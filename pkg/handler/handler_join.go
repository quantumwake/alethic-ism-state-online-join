package handler

import (
	"alethic-ism-state-online-join/pkg/correlate"
	"context"
	"encoding/json"
	"fmt"
	"github.com/quantumwake/alethic-ism-core-go/pkg/data/models"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/processor"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/processor/join"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/state"
	"github.com/quantumwake/alethic-ism-core-go/pkg/routing"
	"log"
	"time"
)

func MessageCallback(ctx context.Context, msg routing.MessageEnvelop) {
	ingestedRawData, err := msg.MessageRaw()

	defer func() {
		if err = msg.Ack(ctx); err != nil {
			log.Printf("error acking message: %v, error: %v", ingestedRawData, err)
		}
	}()

	// unmarshal the message into a map object for processing the message ingestedRawData and metadata fields (e.g. binding)
	var ingestedRouteMsg models.RouteMessage
	err = json.Unmarshal(ingestedRawData, &ingestedRouteMsg)
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
		PublishRouteStatus(ctx, ingestedRouteMsg.RouteID, processor.Failed, err.Error(), ingestedRawData)
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
		// such that we can track and join the inbound ingestedRawData by the output state `join key definition`
		var store *correlate.BlockStore
		store, err = blockStoreCache.GetOrSet(psOut.ID, func() (*correlate.BlockStore, error) {
			// Get processor configuration
			config, err := getProcessorJoinConfig(psIn.ProcessorID)
			if err != nil {
				log.Printf("[Handler] Failed to get processor config for %s, using defaults: %v", psIn.ProcessorID, err)
				// Use defaults if we can't get config
				config = join.DefaultWindowConfig()
			}

			// Parse durations from config with safe dereferencing
			var blockWindowTTLStr, blockPartMaxAgeStr string
			var blockCountSoftLimit, blockPartMaxJoinCount int

			if config.BlockWindowTTL != nil {
				blockWindowTTLStr = *config.BlockWindowTTL
			} else {
				blockWindowTTLStr = "1m"
			}

			if config.BlockPartMaxAge != nil {
				blockPartMaxAgeStr = *config.BlockPartMaxAge
			} else {
				blockPartMaxAgeStr = "15s"
			}

			if config.BlockCountSoftLimit != nil {
				blockCountSoftLimit = *config.BlockCountSoftLimit
			} else {
				blockCountSoftLimit = 10
			}

			if config.BlockPartMaxJoinCount != nil {
				blockPartMaxJoinCount = *config.BlockPartMaxJoinCount
			} else {
				blockPartMaxJoinCount = 1
			}

			blockWindowTTL := parseDurationWithDefault(blockWindowTTLStr, 1*time.Minute)
			blockPartMaxAge := parseDurationWithDefault(blockPartMaxAgeStr, 15*time.Second)

			// Directly fetch join key definitions for the output state
			// This is more efficient than loading the full state object
			joinKeys, err := stateBackend.FindStateConfigKeyDefinitionsByType(psOut.StateID, state.DefinitionStateJoinKey)
			if err != nil {
				return nil, fmt.Errorf("error fetching join keys for state '%s': %v", psOut.StateID, err)
			}
			if len(joinKeys) == 0 {
				return nil, fmt.Errorf("no join keys defined for state '%s'", psOut.StateID)
			}

			LogBlockStoreConfig(psOut.ID, psOut.StateID, psIn.ProcessorID, joinKeys,
				blockCountSoftLimit, blockWindowTTL, blockPartMaxJoinCount, blockPartMaxAge)

			return correlate.NewBlockStore(joinKeys,
				blockCountSoftLimit,
				blockPartMaxJoinCount,
				blockWindowTTL,
				blockPartMaxAge), nil
		})

		// if there is an error publish and move on
		if err != nil {
			PublishRouteStatus(ctx, ingestedRouteMsg.RouteID, processor.Failed, err.Error(), ingestedRawData)
			continue
		}

		// The input ingestedRawData needs to be added to the Block->BlockData[keyValue].DataBySource[source_route_id]
		// TODO IMPORTANT if we want to distribute this, we need to have a distributed cache of sorts
		for idx, queryState := range ingestedRouteMsg.QueryState {
			// Log the incoming ingestedRawData being processed
			joinKeyValue, _ := store.GetJoinKeyValue(queryState)
			log.Print(LogProcessingData(idx+1, len(ingestedRouteMsg.QueryState),
				ingestedRouteMsg.RouteID, psOut.ID, joinKeyValue))

			// the route id defines the source to add the ingestedRawData to
			if err = store.AddData(ingestedRouteMsg.RouteID, queryState, func(data models.Data) error {
				PublishStateSync(ctx, psOut.ID, []models.Data{data})
				return nil
			}); err != nil {
				log.Print(LogDataError(ingestedRouteMsg.RouteID, joinKeyValue, err))
				PublishRouteStatus(ctx, ingestedRouteMsg.RouteID, processor.Failed, err.Error(), ingestedRawData)
			}
		}
	}
}
