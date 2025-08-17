package handler

import (
	"alethic-ism-state-online-join/pkg/correlate"
	"context"
	"encoding/json"
	"fmt"
	"github.com/quantumwake/alethic-ism-core-go/pkg/data"
	"github.com/quantumwake/alethic-ism-core-go/pkg/data/models"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/processor"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/processor/join"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/route"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/state"
	"github.com/quantumwake/alethic-ism-core-go/pkg/routing"
	rnats "github.com/quantumwake/alethic-ism-core-go/pkg/routing/nats"
	"log"
	"os"
	"sync"
	"time"
)

var (
	subscriberRoute  routing.Route // the route we are listening on
	monitorRoute     routing.Route // route for sending errors
	syncRoute        routing.Route // route for sending sync messages
	backendState     = state.NewBackend(os.Getenv("DSN"))
	routeBackend     = route.NewBackend(os.Getenv("DSN"))
	processorBackend = processor.NewBackend(os.Getenv("DSN"))

	// Cache for processor configurations to avoid repeated DB lookups
	processorConfigCache = make(map[string]*join.WindowConfig)
	processorConfigMu    sync.RWMutex

	// Clean up BlockStores that haven't been accessed for 2 hours
	// Check every 5 minutes - no need to check too frequently
	blockStoreCache = correlate.NewCacheBlockStoreWithConfig(2*time.Hour, 5*time.Minute)
	//routingConfig *rnats.Config
	//logger = slog.New(slog.NewTextHandler(os.Stdout, utils.StringFromEnvWithDefault("LOG_LEVEL", "DEBUG")))
)

// getProcessorJoinConfig fetches and parses the join window configuration from processor properties
func getProcessorJoinConfig(processorID string) (*join.WindowConfig, error) {
	// Check cache first
	processorConfigMu.RLock()
	if config, exists := processorConfigCache[processorID]; exists {
		processorConfigMu.RUnlock()
		return config, nil
	}
	processorConfigMu.RUnlock()

	// Fetch processor from database
	proc, err := processorBackend.FindProcessorByID(processorID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch processor %s: %v", processorID, err)
	}

	// Parse configuration from properties
	config := parseJoinWindowConfig(proc.Properties)

	// Cache the configuration
	processorConfigMu.Lock()
	processorConfigCache[processorID] = config
	processorConfigMu.Unlock()

	return config, nil
}

// parseJoinWindowConfig extracts join window configuration from processor properties
func parseJoinWindowConfig(properties *data.JSON) *join.WindowConfig {
	// Default configuration
	config := join.DefaultWindowConfig()

	if properties == nil || *properties == nil {
		return config
	}

	props := *properties

	// Extract blockCountSoftLimit
	if val, ok := props["blockCountSoftLimit"]; ok {
		if limit, ok := val.(float64); ok {
			config.BlockCountSoftLimit = int(limit)
		}
	}

	// Extract blockWindowTTL
	if val, ok := props["blockWindowTTL"]; ok {
		if ttl, ok := val.(string); ok {
			config.BlockWindowTTL = ttl
		}
	}

	// Extract blockPartMaxJoinCount
	if val, ok := props["blockPartMaxJoinCount"]; ok {
		if count, ok := val.(float64); ok {
			config.BlockPartMaxJoinCount = int(count)
		}
	}

	// Extract blockPartMaxAge
	if val, ok := props["blockPartMaxAge"]; ok {
		if age, ok := val.(string); ok {
			config.BlockPartMaxAge = age
		}
	}

	return config
}

// parseDurationWithDefault parses a duration string and returns default if parsing fails
func parseDurationWithDefault(durationStr string, defaultDuration time.Duration) time.Duration {
	if duration, err := time.ParseDuration(durationStr); err == nil {
		return duration
	}
	log.Printf("[Handler] Failed to parse duration '%s', using default: %v", durationStr, defaultDuration)
	return defaultDuration
}

const (
	SelectorSubscriber = "data/transformers/mixer/state-online-join-1.0"
	SelectorMonitor    = "processor/monitor"
	SelectorStoreSync  = "processor/state/sync"
)

func Teardown(ctx context.Context) {
	// Shutdown the block store cache
	if blockStoreCache != nil {
		blockStoreCache.Shutdown()
	}

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
	var ingestedRouteMsg models.RouteMessage
	err = json.Unmarshal(data, &ingestedRouteMsg)
	if err != nil {
		PublishRouteStatus(ctx, ingestedRouteMsg.RouteID, processor.Failed, err.Error(), ingestedRouteMsg.QueryState)
		return
	}

	// set route running, defer completed status update.
	// TODO IMPORTANT FOR SCALING in the case of replicas > 1, or too many inbound messages....
	//  really need to figure this out, there is definitely a race condition and too many messages to be updated.
	//  for each record that comes into this it will create a new status update, this is unacceptable, and would quickly
	//  escalate to a large number of messages being sent to the monitor route.
	PublishRouteStatus(ctx, ingestedRouteMsg.RouteID, processor.Running, "", nil)
	defer func() {
		if panicErr := recover(); panicErr != nil {
			PublishRouteStatus(ctx, ingestedRouteMsg.RouteID, processor.Failed, fmt.Sprintf("panic error: %v", panicErr), ingestedRouteMsg.QueryState)
		} else {
			PublishRouteStatus(ctx, ingestedRouteMsg.RouteID, processor.Completed, "", nil)
		}
	}()

	// TODO IMPORTANT needs to be cached.
	// for given input route fetch all routes, specifically requiring the output routes
	psIn, psOuts, err := routeBackend.FindRouteWithOutputsByID(ingestedRouteMsg.RouteID)
	if err != nil {
		log.Printf("error finding route and outputs for route ID: %v, err: %v", ingestedRouteMsg.RouteID, err)
		PublishRouteStatus(ctx, ingestedRouteMsg.RouteID, processor.Failed, err.Error(), data)
		// TODO probably should not try and process this over and over again if its the same block psIn that is failing
		return
	}

	// get the block from the cache or create a new block if it doesn't exist
	// TODO in a distributed environment we need to have a distributed cache or
	//  somehow ensure that the block is always consumed by the same instance,
	//  while still maintaining a global cache, in the event of a L2 cache miss

	// for each of the output route, create a new sliding window cache block
	for _, psOut := range psOuts {

		// get or create a sliding window cache store for each output route
		// such that we can track and join the inbound data by the output state `join key definition`
		var store *correlate.BlockStore
		store, err = blockStoreCache.GetOrSet(psOut.ID, func() (*correlate.BlockStore, error) {
			// Get processor configuration
			config, err := getProcessorJoinConfig(psIn.ProcessorID)
			if err != nil {
				log.Printf("[Handler] Failed to get processor config for %s, using defaults: %v", psIn.ProcessorID, err)
				// Use defaults if we can't get config
				config = join.DefaultWindowConfig()
			}

			// Parse durations from config
			blockWindowTTL := parseDurationWithDefault(config.BlockWindowTTL, 1*time.Minute)
			blockPartMaxAge := parseDurationWithDefault(config.BlockPartMaxAge, 15*time.Second)

			// Directly fetch join key definitions for the output state
			// This is more efficient than loading the full state object
			joinKeys, err := backendState.FindStateConfigKeyDefinitionsByType(psOut.StateID, state.DefinitionStateJoinKey)
			if err != nil {
				return nil, fmt.Errorf("error fetching join keys for state '%s': %v", psOut.StateID, err)
			}
			if len(joinKeys) == 0 {
				return nil, fmt.Errorf("no join keys defined for state '%s'", psOut.StateID)
			}

			LogBlockStoreConfig(psOut.ID, psOut.StateID, psIn.ProcessorID, joinKeys,
				config.BlockCountSoftLimit, blockWindowTTL, config.BlockPartMaxJoinCount, blockPartMaxAge)

			return correlate.NewBlockStore(joinKeys,
				config.BlockCountSoftLimit,
				config.BlockPartMaxJoinCount,
				blockWindowTTL,
				blockPartMaxAge), nil
		})

		// if there is an error publish and move on
		if err != nil {
			PublishRouteStatus(ctx, ingestedRouteMsg.RouteID, processor.Failed, err.Error(), data)
			continue
		}

		// The input data needs to be added to the Block->BlockData[keyValue].DataBySource[source_route_id]
		// TODO IMPORTANT if we want to distribute this, we need to have a distributed cache of sorts
		for idx, queryState := range ingestedRouteMsg.QueryState {
			// Log the incoming data being processed
			joinKeyValue, _ := store.GetJoinKeyValue(queryState)
			log.Print(LogProcessingData(idx+1, len(ingestedRouteMsg.QueryState), 
				ingestedRouteMsg.RouteID, psOut.ID, joinKeyValue))

			// the route id defines the source to add the data to
			if err = store.AddData(ingestedRouteMsg.RouteID, queryState, func(data models.Data) error {
				PublishStateSync(ctx, psOut.ID, []models.Data{data})
				return nil
			}); err != nil {
				log.Print(LogDataError(ingestedRouteMsg.RouteID, joinKeyValue, err))
				PublishRouteStatus(ctx, ingestedRouteMsg.RouteID, processor.Failed, err.Error(), data)
			}
		}
	}
}
