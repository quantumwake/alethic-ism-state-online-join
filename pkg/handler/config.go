package handler

import (
	"encoding/json"
	"fmt"
	"github.com/quantumwake/alethic-ism-core-go/pkg/data"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/processor/join"
	"log"
	"time"
)

// getProcessorJoinConfig fetches and parses the join window configuration from processor properties
func getProcessorJoinConfig(processorID string) (*join.WindowConfig, error) {
	// Fetch processor from database
	proc, err := processorBackend.FindProcessorByID(processorID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch processor %s: %v", processorID, err)
	}

	// Parse configuration from properties
	config, err := parseWindowConfig(proc.Properties)
	if err != nil {
		return nil, fmt.Errorf("failed to parse window config: %v", err)
	}
	return config, nil
}

// parseWindowConfig extracts window configuration from processor properties using JSON unmarshaling
func parseWindowConfig(properties *data.JSON) (*join.WindowConfig, error) {
	// Start with default configuration
	config := join.DefaultWindowConfig()

	if properties == nil || *properties == nil {
		return config, nil
	}

	// Marshal properties to JSON bytes
	bytes, err := json.Marshal(*properties)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal properties: %v", err)
	}

	// Unmarshal directly into config - nil fields won't be overwritten
	if err := json.Unmarshal(bytes, config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal window config: %v", err)
	}

	return config, nil
}

// parseDurationWithDefault parses a duration string and returns default if parsing fails
func parseDurationWithDefault(durationStr string, defaultDuration time.Duration) time.Duration {
	if duration, err := time.ParseDuration(durationStr); err == nil {
		return duration
	}
	log.Printf("[Handler] Failed to parse duration '%s', using default: %v", durationStr, defaultDuration)
	return defaultDuration
}
