package main

import (
	"alethic-ism-state-online-join/pkg/correlate"
	"context"
	"encoding/json"
	"fmt"
	"github.com/nats-io/nats.go"
	"github.com/quantumwake/alethic-ism-core-go/pkg/data/models"
	"github.com/quantumwake/alethic-ism-core-go/pkg/data/models/processor"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/route"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/state"
	"github.com/quantumwake/alethic-ism-core-go/pkg/routing"
	"github.com/quantumwake/alethic-ism-core-go/pkg/routing/config"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var (
	natsRoute *routing.NATSRoute // the route we are listening on
	//messageStore         *store.CorrelateBlock
	natsRouteStateRouterV1 *routing.NATSRoute // route for routing state messages
	natsRouteMonitor       *routing.NATSRoute // route for sending errors
	natsRouteStateSync     *routing.NATSRoute // route for sending sync messages
	backendState           = state.NewBackend(os.Getenv("DSN"))
	routeBackend           = route.NewBackend(os.Getenv("DSN"))

	blockCache    = correlate.NewCacheBlock()
	routingConfig *config.Config
	//logger = slog.New(slog.NewTextHandler(os.Stdout, utils.StringFromEnvWithDefault("LOG_LEVEL", "DEBUG")))
)

func onMessageReceived(ctx context.Context, route *routing.NATSRoute, msg *nats.Msg) {
	defer func(msg *nats.Msg, opts ...nats.AckOpt) {
		err := msg.Ack()
		if err != nil {
			log.Printf("error acking message: %v, error: %v", msg.Data, err)
		}
	}(msg)

	// unmarshal the message into a map object for processing the message data and metadata fields (e.g. binding)
	//var jsonMsg map[string]interface{}
	var routeMsg models.RouteMessage
	err := json.Unmarshal(msg.Data, &routeMsg)
	if err != nil {
		PublishRouteStatus(ctx, routeMsg.RouteID, processor.Failed, err.Error(), routeMsg.QueryState)
		return
	}

	// set route running, defer completed status update.
	///
	// TODO IMPORTANT FOR SCALING in the case of replicas > 1, or too many inbound messages....
	//  really need to figure this out, there is definitely a race condition and too many messages to be updated.
	//  for each record that comes into this it will create a new status update, this is unacceptable, and would quickly
	//  escalate to a large number of messages being sent to the monitor route.
	PublishRouteStatus(ctx, routeMsg.RouteID, processor.Running, "", nil)
	defer func() {
		if panicErr := recover(); panicErr != nil {
			PublishRouteStatus(ctx, routeMsg.RouteID, processor.Failed, fmt.Sprintf("panic error: %v", panicErr), routeMsg.QueryState)
		} else {
			PublishRouteStatus(ctx, routeMsg.RouteID, processor.Completed, "", nil)
		}
	}()

	// Use "psIn" as the correlation key field.
	//keyFields := []string{"psIn"}
	softMaxThreshold := 10
	softWindow := 10 * time.Second
	hardWindow := 30 * time.Second

	// TODO IMPORTANT needs to be cached.
	// give the route the data was received on, pull the route information such that we can identify the processor id
	psIn, err := routeBackend.FindRouteByID(routeMsg.RouteID)
	if err != nil {
		return
	}

	// TODO IMPORTANT needs to be cached.
	// given the processor id the data is destined for, find all output routes such that we can iterate each output and write it separately.
	psOuts, err := routeBackend.FindRouteByProcessorAndDirection(psIn.ProcessorID, processor.DirectionOutput)
	if err != nil {
		log.Printf("error finding output states for processor: %v, err: %v", psIn.ProcessorID, err)
		PublishRouteStatus(ctx, routeMsg.RouteID, processor.Failed, err.Error(), msg.Data)
		// TODO probably should not try and process this over and over again if its the same block psIn that is failing
		return
	}

	// get the block from the cache or create a new block if it doesn't exist
	// TODO in a distributed environment we need to have a distributed cache or
	//  somehow ensure that the block is always consumed by the same instance,
	//  while still maintaining a global cache, in the event of a L2 cache miss

	// for each of the processors output, create a new sliding window cache block
	for _, psOut := range psOuts {

		// get or create a sliding window cache block for each output state such that we can track inbound data and join them by the output primary key definition
		var block *correlate.Block
		block, err = blockCache.GetOrSet(psOut.ID, func() (*correlate.Block, error) {
			// fetch the key definitions such that we can join the data on that primary keys defined in the state output
			var outputState *state.State
			outputState, err = backendState.FindStateFull(psOut.StateID, state.StateLoadBasic|state.StateLoadConfigKeyDefinitions)
			if err != nil {
				return nil, err
			}

			// pull the primary keys
			joinKeys := outputState.Config.GetKeyDefinitionsByType(state.DefinitionStateJoinKey)
			if joinKeys == nil {
				return nil, fmt.Errorf("no join keys defined for state '%s'", psOut.StateID)
			}

			return correlate.NewBlock(joinKeys, softMaxThreshold, softWindow, hardWindow), nil
		})

		// if there is an error publish and move on
		if err != nil {
			PublishRouteStatus(ctx, routeMsg.RouteID, processor.Failed, err.Error(), msg.Data)
			continue
		}

		// add the the query state rows to the block (given processor psIn it is executing on)
		// TODO IMPORTANT if we want to distribute this, we need to have a distributed cache of sorts
		for _, queryState := range routeMsg.QueryState {
			if err = block.AddData(routeMsg.RouteID, queryState, func(data models.Data) error {
				PublishStateSync(ctx, psOut.ID, []models.Data{data})
				return nil
			}); err != nil {
				PublishRouteStatus(ctx, routeMsg.RouteID, processor.Failed, err.Error(), msg.Data)
			}
		}
	}

	//states, _ := routeBackend.FindRouteByProcessorAndDirection(processorID, processor_state.DirectionOutput)
	//
	//for _, state := range states {
	//
	//	block := correlate.NewBlock("")
	//}
	//
	//if routeMsg.QueryState == nil {
	//	handleError(routeMsg, ctx, fmt.Errorf("error: query state not found in message"))
	//	return
	//}

	// iterate over the query state entries and process them
	//for _, qse := range routeMsg.QueryState {
	//	handleQueryState(routeMsg, qse)
	//}

}

func PublishRouteStatus(ctx context.Context, routeID string, status processor.ProcessorStatus, exception string, data interface{}) {
	monitorMessage := processor.MonitorMessage{
		Type:      models.MonitorProcessorState,
		RouteID:   routeID,
		Status:    status,
		Exception: exception,
		Data:      data,
	}

	log.Printf("Sending monitor route: %v, status: %v\n", routeID, status)

	err := natsRouteMonitor.Publish(ctx, monitorMessage)
	if err != nil {
		// TODO need to log this error with proper error handling and logging
		log.Print("critical error: unable to publish error to monitor route")
	}

	err = natsRouteMonitor.Flush()
	if err != nil {
		// TODO need to log this error with proper error handling and logging
		log.Print("critical error: unable to publish error to monitor route")
	}
}

func PublishStateSync(ctx context.Context, routeID string, queryState []models.Data) {
	syncMessage := models.RouteMessage{
		Type:       models.QueryStateRoute,
		RouteID:    routeID,
		QueryState: queryState,
	}

	log.Printf("Sending state to state sync route: %v\n", routeID)

	err := natsRouteStateSync.Publish(ctx, syncMessage)
	if err != nil {
		// TODO need to log this error with proper error handling and logging
		log.Print("critical error: unable to publish error to state sync route")
	}

	err = natsRouteStateSync.Flush()
	if err != nil {
		// TODO need to log this error with proper error handling and logging
		log.Print("critical error: unable to publish error to state sync route")
	}
}

//func handleError(routeMsg models.RouteMessage, ctx context.Context, err error) {
//	handleErrorWithParams(routeMsg.RouteID, routeMsg.QueryState, ctx, err)
//}

func PublishStatusUpdateWithErrorMsg(ctx context.Context, routeID string, dataValue interface{}, err error) {
	err = fmt.Errorf("error unmarshalling json object: %v", err)
	monitorMessage := processor.MonitorMessage{
		Type:      models.MonitorProcessorState,
		RouteID:   routeID,
		Status:    processor.Failed,
		Exception: err.Error(),
		Data:      dataValue,
	}

	err = natsRouteMonitor.Publish(ctx, monitorMessage)
	if err != nil {
		// TODO need to log this error with proper error handling and logging
		log.Print("Critical error: unable to publish error to monitor route")
	}
}

func InitializeRouteWithCallback(ctx context.Context, selector string, callback func(ctx context.Context, route *routing.NATSRoute, msg *nats.Msg)) *routing.NATSRoute {
	var err error
	if routingConfig == nil {
		if routingConfig, err = config.LoadConfigFromEnv(); err != nil {
			log.Fatalf("error loading routing config: %v", err)
		}
	}

	//// find monitor route, used for sending errors
	routeConfig, err := routingConfig.FindRouteBySelector(selector)

	//monitorRoute, err := routes.FindRouteBySelector("processor/monitor")
	if err != nil {
		log.Fatalf("error finding monitor route: %v", err)
	}

	if callback == nil {
		return routing.NewNATSRoute(routeConfig)
	}

	// otherwise subscribe to the route with the callback for when messages are received
	natsRoute = routing.NewNATSRouteWithCallback(routeConfig, callback)
	if err = natsRoute.Connect(context.Background()); err != nil {
		log.Fatalf("error connecting to monitor route: %v", err)
	}

	// subscribe to the route with the callback for when messages are received
	err = natsRoute.Subscribe(ctx)
	if err != nil {
		log.Fatalf("unable to subscribe to NATS: %v, route: %v", routeConfig, err)
	}

	return natsRoute
}

func main() {
	var err error
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		log.Println("Received termination signal")
		err = natsRoute.Unsubscribe(ctx)
		if err != nil {
			return
		}
		log.Println("Gracefully shut down")
	}()

	// set up listener route such that events can be received from the NATS server and processed
	listenerRoute := InitializeRouteWithCallback(ctx, "data/transformers/mixer/state-online-join-1.0", onMessageReceived)
	log.Printf("Listening on route: %v\n", listenerRoute.Config.Subject)

	// setup other require routes, monitor, state sync, state router
	natsRouteMonitor = InitializeRouteWithCallback(ctx, "processor/monitor", nil)
	natsRouteStateSync = InitializeRouteWithCallback(ctx, "processor/state/sync", nil)
	natsRouteStateRouterV1 = InitializeRouteWithCallback(ctx, "processor/state/router", nil)

	// set up process signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan // wait on signal

	//
	//// find the state router route
	//stateRouter, err := routes.FindRouteBySelector("processor/state/router")
	//if err != nil {
	//	log.Fatalf("error finding state router route: %v", err)
	//}
	//natsRouteStateRouter = routing.NewNATSRoute(stateRouter)
	//

	//
	//// connect to monitor route
	//stateSyncRoute, err := routes.FindRouteBySelector("processor/state/sync")
	//natsRouteStateSync = routing.NewNATSRoute(stateSyncRoute)
	//err = natsRouteStateSync.Connect(ctx)
	//if err != nil {
	//	log.Fatalf("error connecting to state sync route: %v", err)
	//}

	// connect to usage database
	//dsn, ok := os.LookupEnv("DSN")
	//if !ok {
	//	log.Fatalf("DSN environment variable not set")
	//}
	//dataAccess := data.NewDataAccess(dsn)

	// TODO need to have graceful shutdown here
	//select {}

	//// Wait for termination signal
	//<-sigChan
	//log.Println("Received termination signal")
	//
	//// Cancel the context to stop the reconcile loop
	//cancel()
	//
	//// Wait for the reconcile loop to finish
	//<-store.RunningDone
}
