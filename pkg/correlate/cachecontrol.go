package correlate

import "time"

// CacheControlContext carries the per-processor, per-message resolved knobs that control
// how long source values are retained and how many times they may join on a key. It is
// resolved from the routed-to processor's properties (⊕ system defaults, clamped by system
// ceilings) and injected into AddData per message — so a properties change takes effect on
// the next event, without rebuilding the BlockStore.
//
// It deliberately does NOT carry core cache limits (block-count soft limit, backend/store
// cache TTLs): those are operator/system controlled, not per-processor.
type CacheControlContext struct {
	ProcessorID string // the processor the message is routed to (psIn.ProcessorID)

	BlockWindowTTL        time.Duration // how long a key/block stays alive (sliding; resets per event)
	BlockPartMaxAge       time.Duration // how long a single source value is retained
	BlockPartMaxJoinCount int           // how many times a value may join; 0 = unlimited (N:M)

	// MinSources gates emission: no join is emitted for a key until at least this many
	// distinct sources have a live part. Floor is 2 (a join needs two sources); the handler
	// applies that floor. 0 disables the gate (engine default; natural behavior still needs
	// two sources to produce any pair).
	MinSources int
}
