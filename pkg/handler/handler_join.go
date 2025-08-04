package handler

import (
	"alethic-ism-state-online-join/pkg/correlate"
	"context"
	"encoding/json"
	"fmt"
	"github.com/quantumwake/alethic-ism-core-go/pkg/data/models"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/processor"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/route"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/state"
	"github.com/quantumwake/alethic-ism-core-go/pkg/routing"
	rnats "github.com/quantumwake/alethic-ism-core-go/pkg/routing/nats"
	"log"
	"os"
	"time"
)

var (
	subscriberRoute routing.Route // the route we are listening on
	monitorRoute    routing.Route // route for sending errors
	syncRoute       routing.Route // route for sending sync messages
	backendState    = state.NewBackend(os.Getenv("DSN"))
	routeBackend    = route.NewBackend(os.Getenv("DSN"))

	blockCache = correlate.NewCacheBlock()
	//routingConfig *rnats.Config
	//logger = slog.New(slog.NewTextHandler(os.Stdout, utils.StringFromEnvWithDefault("LOG_LEVEL", "DEBUG")))
)

const (
	SelectorSubscriber = "data/transformers/mixer/state-online-join-1.0"
	SelectorMonitor    = "processor/monitor"
	SelectorStoreSync  = "processor/state/sync"
)

func Teardown(ctx context.Context) {
	if err := subscriberRoute.Unsubscribe(ctx); err != nil {
		return
	}

	if err := subscriberRoute.Disconnect(ctx); err != nil {
		panic(err)
	}

	if err := syncRoute.Disconnect(ctx); err != nil {
		panic(err)
	}

	if err := monitorRoute.Disconnect(ctx); err != nil {
		panic(err)
	}

}

func Startup(ctx context.Context) {
	var err error

	// set up listener route such that events can be received from the NATS server and processed
	subscriberRoute, err = rnats.NewRouteSubscriberUsingSelector(ctx, SelectorSubscriber, MessageCallback)
	if err != nil {
		log.Fatalf("unable to create nats route subscriber: %v", err)
	}

	// setup other require routes, monitor, state sync, state router
	monitorRoute, err = rnats.NewRouteUsingSelector(ctx, SelectorMonitor)
	if err != nil {
		log.Fatalf("unable to create nats route: %v", err)
	}

	syncRoute, err = rnats.NewRouteUsingSelector(ctx, SelectorStoreSync)
	if err != nil {
		log.Fatalf("unable to initialize route: %v", err)
	}
}

func MessageCallback(ctx context.Context, msg routing.MessageEnvelop) {
	data, err := msg.MessageRaw()

	defer func() {
		if err = msg.Ack(ctx); err != nil {
			log.Printf("error acking message: %v, error: %v", data, err)
		}
	}()

	// unmarshal the message into a map object for processing the message data and metadata fields (e.g. binding)
	//var jsonMsg map[string]interface{}
	var routeMsg models.RouteMessage
	err = json.Unmarshal(data, &routeMsg)
	if err != nil {
		PublishRouteStatus(ctx, routeMsg.RouteID, processor.Failed, err.Error(), routeMsg.QueryState)
		return
	}

	// set route running, defer completed status update.
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
	softWindow := 1 * time.Minute
	hardWindow := 1 * time.Minute

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
		PublishRouteStatus(ctx, routeMsg.RouteID, processor.Failed, err.Error(), data)
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
			PublishRouteStatus(ctx, routeMsg.RouteID, processor.Failed, err.Error(), data)
			continue
		}

		// add the the query state rows to the block (given processor psIn it is executing on)
		// TODO IMPORTANT if we want to distribute this, we need to have a distributed cache of sorts
		for _, queryState := range routeMsg.QueryState {
			if err = block.AddData(routeMsg.RouteID, queryState, func(data models.Data) error {
				PublishStateSync(ctx, psOut.ID, []models.Data{data})
				return nil
			}); err != nil {
				PublishRouteStatus(ctx, routeMsg.RouteID, processor.Failed, err.Error(), data)
			}
		}
	}
}
