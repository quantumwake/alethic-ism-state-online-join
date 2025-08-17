package handler

import (
	"fmt"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/state"
	"strings"
	"time"
)

// FormatJoinKeyDefinitions formats join key definitions for logging
func FormatJoinKeyDefinitions(keyDefs state.ColumnKeyDefinitions) string {
	parts := make([]string, len(keyDefs))
	for i, keyDef := range keyDefs {
		// Include field name and alias if available
		if keyDef.Alias != "" {
			parts[i] = fmt.Sprintf("%s(alias:%s)", keyDef.Name, keyDef.Alias)
		} else {
			parts[i] = keyDef.Name
		}
	}
	return strings.Join(parts, ", ")
}

// LogBlockStoreConfig logs the configuration when creating a new BlockStore
func LogBlockStoreConfig(routeID, stateID, processorID string, joinKeys state.ColumnKeyDefinitions,
	blockCountSoftLimit int, blockWindowTTL time.Duration, blockPartMaxJoinCount int, blockPartMaxAge time.Duration) {
	
	joinKeyStr := FormatJoinKeyDefinitions(joinKeys)
	
	fmt.Printf("[Handler] Creating new BlockStore for route: %s (state: %s, processor: %s)\n", 
		routeID, stateID, processorID)
	fmt.Printf("[Handler] BlockStore config - JoinKeys: [%s] | CountSoftLimit: %d | WindowTTL: %v | PartMaxJoinCount: %d | PartMaxAge: %v\n",
		joinKeyStr, blockCountSoftLimit, blockWindowTTL, blockPartMaxJoinCount, blockPartMaxAge)
}

// LogProcessingData logs when data is being processed through the join handler
func LogProcessingData(index, total int, routeID, outputRouteID, joinKey string) string {
	return fmt.Sprintf("[Handler] Processing data %d/%d - RouteID: %s | OutputRoute: %s | JoinKey: %s",
		index, total, routeID, outputRouteID, joinKey)
}

// LogDataError logs when an error occurs while adding data
func LogDataError(routeID, joinKey string, err error) string {
	return fmt.Sprintf("[Handler] Error adding data - RouteID: %s | JoinKey: %s | Error: %v",
		routeID, joinKey, err)
}