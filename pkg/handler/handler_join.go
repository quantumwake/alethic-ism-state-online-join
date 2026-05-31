package handler

import (
	"alethic-ism-state-online-join/pkg/correlate"
	"context"
	"encoding/json"
	"fmt"
	"github.com/quantumwake/alethic-ism-core-go/pkg/data/models"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/processor"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/state"
	"github.com/quantumwake/alethic-ism-core-go/pkg/routing"
	"log"
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

	// Resolve the per-message cache-control knobs from the processor this message is routed
	// to (psIn.ProcessorID). Resolved once per message; the same context applies to every
	// output. Served from the backend metadata cache, so a properties edit takes effect
	// within BACKEND_CACHE_TTL — no BlockStore rebuild required.
	ccc := resolveCacheControl(psIn.ProcessorID)

	// for each output state, create/lookup its sliding-window cache store
	for _, psOut := range psOuts {

		// The store holds only structural identity (join key definitions) + the system
		// block-count limit; the per-event knobs are supplied per call via ccc.
		var store *correlate.BlockStore
		store, err = blockStoreCache.GetOrSet(psOut.ID, func() (*correlate.BlockStore, error) {
			// Directly fetch join key definitions for the output state
			joinKeys, err := stateBackend.FindStateConfigKeyDefinitionsByType(psOut.StateID, state.DefinitionStateJoinKey)
			if err != nil {
				return nil, fmt.Errorf("error fetching join keys for state '%s': %v", psOut.StateID, err)
			}
			if len(joinKeys) == 0 {
				return nil, fmt.Errorf("no join keys defined for state '%s'", psOut.StateID)
			}

			LogBlockStoreConfig(psOut.ID, psOut.StateID, psIn.ProcessorID, joinKeys, systemBlockCountSoftLimit)

			return correlate.NewBlockStore(joinKeys, systemBlockCountSoftLimit), nil
		})

		// if there is an error publish and move on
		if err != nil {
			PublishRouteStatus(ctx, ingestedRouteMsg.RouteID, processor.Failed, err.Error(), ingestedRawData)
			continue
		}

		// The input ingestedRawData is added to block.partsBySource[source_route_id]
		// TODO IMPORTANT if we want to distribute this, we need to have a distributed cache of sorts
		for idx, queryState := range ingestedRouteMsg.QueryState {
			// Log the incoming ingestedRawData being processed
			joinKeyValue, _ := store.GetJoinKeyValue(queryState)
			log.Print(LogProcessingData(idx+1, len(ingestedRouteMsg.QueryState),
				ingestedRouteMsg.RouteID, psOut.ID, joinKeyValue))

			// the route id defines the source to add the ingestedRawData to
			if err = store.AddData(ccc, ingestedRouteMsg.RouteID, queryState, func(data models.Data) error {
				PublishStateSync(ctx, psOut.ID, []models.Data{data})
				return nil
			}); err != nil {
				log.Print(LogDataError(ingestedRouteMsg.RouteID, joinKeyValue, err))
				PublishRouteStatus(ctx, ingestedRouteMsg.RouteID, processor.Failed, err.Error(), ingestedRawData)
			}
		}
	}
}
