package handler

import (
	"alethic-ism-state-online-join/pkg/correlate"
	"encoding/json"
	"fmt"
	"github.com/quantumwake/alethic-ism-core-go/pkg/data"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/processor/join"
	"log"
	"time"
)

// resolveCacheControl builds the per-message CacheControlContext for the processor the
// inbound message is routed to. Window config is read from the processor's properties
// (defaults applied), then retention values are clamped by the system MAX_RETENTION ceiling.
// This runs per message (served from the backend metadata cache), so a properties edit
// takes effect within BACKEND_CACHE_TTL without rebuilding the BlockStore.
func resolveCacheControl(processorID string) correlate.CacheControlContext {
	cfg := join.DefaultWindowConfig()
	minSources := DefaultMinSources

	proc, err := processorBackend.FindProcessorByID(processorID)
	if err != nil {
		log.Printf("[Handler] cache-control: processor %s lookup failed, using defaults: %v", processorID, err)
	} else {
		if c, perr := parseWindowConfig(proc.Properties); perr != nil {
			log.Printf("[Handler] cache-control: window config parse failed for %s, using defaults: %v", processorID, perr)
		} else {
			cfg = c
		}
		minSources = intFromProperties(proc.Properties, "minSources", DefaultMinSources)
	}

	// Floor: a join needs at least two distinct sources.
	if minSources < DefaultMinSources {
		minSources = DefaultMinSources
	}

	windowTTL := clampRetention(parseDurationWithDefault(derefString(cfg.BlockWindowTTL, "1m"), time.Minute))
	maxAge := clampRetention(parseDurationWithDefault(derefString(cfg.BlockPartMaxAge, "15s"), 15*time.Second))

	maxJoin := 1 // default: one-to-one
	if cfg.BlockPartMaxJoinCount != nil {
		maxJoin = *cfg.BlockPartMaxJoinCount
	}

	return correlate.CacheControlContext{
		ProcessorID:           processorID,
		BlockWindowTTL:        windowTTL,
		BlockPartMaxAge:       maxAge,
		BlockPartMaxJoinCount: maxJoin,
		MinSources:            minSources,
	}
}

// clampRetention bounds a retention duration by the system MAX_RETENTION ceiling so a
// processor can't pin data in memory indefinitely.
func clampRetention(d time.Duration) time.Duration {
	if systemMaxRetention > 0 && d > systemMaxRetention {
		log.Printf("[Handler] retention %v exceeds %s=%v; clamping to ceiling", d, EnvMaxRetention, systemMaxRetention)
		return systemMaxRetention
	}
	return d
}

// derefString returns *s when set and non-empty, otherwise def.
func derefString(s *string, def string) string {
	if s != nil && *s != "" {
		return *s
	}
	return def
}

// intFromProperties reads an int-valued property from a processor's Properties JSON,
// falling back to def when absent or not a number. (JSON numbers decode to float64.)
func intFromProperties(props *data.JSON, key string, def int) int {
	if props == nil || *props == nil {
		return def
	}
	switch n := (*props)[key].(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return def
	}
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
