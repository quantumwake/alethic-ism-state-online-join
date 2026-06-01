package handler

import (
	"alethic-ism-state-online-join/pkg/correlate"
	"context"
	"github.com/quantumwake/alethic-ism-core-go/pkg/cache"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/processor"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/route"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/state"
	"github.com/quantumwake/alethic-ism-core-go/pkg/routing"
	rnats "github.com/quantumwake/alethic-ism-core-go/pkg/routing/nats"
	"log"
	"os"
	"strconv"
	"time"
)

const (
	SelectorSubscriber = "data/transformers/mixer/state-online-join-1.0"
	SelectorMonitor    = "processor/monitor"
	SelectorStoreSync  = "processor/state/sync"
)

// Tunable cache settings (env var names). These caches are intentionally process-local
// (no central cache yet); per-tenant / per-project scoping will come later.
const (
	EnvBackendCacheTTL        = "BACKEND_CACHE_TTL"            // local DB metadata cache TTL
	EnvBlockStoreIdleTTL      = "BLOCK_STORE_IDLE_TTL"         // idle BlockStore eviction threshold
	EnvBlockStoreCleanupEvery = "BLOCK_STORE_CLEANUP_INTERVAL" // idle-store sweep cadence
	EnvBlockCountSoftLimit    = "BLOCK_COUNT_SOFT_LIMIT"       // core: max blocks (per store) before eviction
	EnvMaxRetention           = "MAX_RETENTION"               // core: ceiling for any per-processor retention knob
)

// Defaults preserve the previously hardcoded values.
const (
	DefaultBackendCacheTTL        = 30 * time.Second
	DefaultBlockStoreIdleTTL      = 2 * time.Hour
	DefaultBlockStoreCleanupEvery = 5 * time.Minute
	DefaultBlockCountSoftLimit    = 10
	DefaultMaxRetention           = 24 * time.Hour
	DefaultMinSources             = 2 // floor + default: a join needs at least two sources
)

// System (core) cache limits — operator-controlled, NOT per-processor. Resolved from env in
// Startup; consumed by NewBlockStore (soft limit) and resolveCacheControl (retention cap).
var (
	systemBlockCountSoftLimit = DefaultBlockCountSoftLimit
	systemMaxRetention        = DefaultMaxRetention
)

// intFromEnv reads an int from an env var, falling back to def when unset/unparseable.
func intFromEnv(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		log.Printf("[Setup] invalid int for %s=%q, using default %d", key, v, def)
	}
	return def
}

// durationFromEnv reads a Go duration string (e.g. "30s", "2h") from an env var,
// falling back to def when unset; an unparseable value also falls back (and logs).
func durationFromEnv(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		return parseDurationWithDefault(v, def)
	}
	return def
}

var (
	dsn = os.Getenv("DSN")

	subscriberRoute routing.Route // the route we are listening on
	monitorRoute    routing.Route // route for sending errors
	syncRoute       routing.Route // route for sending sync messages
	//routeBackend    = route.NewBackend(os.Getenv("DSN"))
	//processorBackend = processor.NewBackend(os.Getenv("DSN"))

	stateBackend = state.NewBackend(os.Getenv("DSN"))

	// backendCache
	backendCache cache.Cache

	//stateBackend *state
	routeBackend     *route.CachedBackendStorage
	processorBackend *processor.CachedBackendStorage

	// BlockStore cache; initialized in Startup so its settings can be read from env.
	blockStoreCache *correlate.CacheBlockStore
	//routingConfig *rnats.Config
	//logger = slog.New(slog.NewTextHandler(os.Stdout, utils.StringFromEnvWithDefault("LOG_LEVEL", "DEBUG")))
)

func SetupCachedBackend() error {
	// Local (per-process) cache for DB route/processor/state lookups. TTL is tunable via
	// env because this is intentionally a local cache (no central cache yet); per-tenant /
	// per-project scoping is a future concern.
	ttl := durationFromEnv(EnvBackendCacheTTL, DefaultBackendCacheTTL)
	// Use NewConfigWithTTL (not a partial &Config{DefaultTTL: ttl}) so CleanupDurationInterval
	// is populated. A bare literal leaves it at 0, and since NewLocalCache only falls back to
	// defaults when the whole config is nil, time.NewTicker(0) in cleanupExpired panics at startup.
	backendCache = cache.NewLocalCache(cache.NewConfigWithTTL(ttl))
	log.Printf("[Setup] backend metadata cache TTL: %v (env %s)", ttl, EnvBackendCacheTTL)

	baseTTL := backendCache.GetDefaultTTL()

	// All backends now respect this TTL
	processorBackend = processor.NewCachedBackend(dsn, backendCache, baseTTL)
	routeBackend = route.NewCachedBackend(dsn, backendCache, baseTTL)

	//cachedBackend := cache.NewCachedBackend(processor.NewBackend(dsn), localCache, 5*time.Minute)
	return nil
}

func Startup(ctx context.Context) {
	var err error
	if err = SetupCachedBackend(); err != nil {
		panic(err)
	}

	// Initialize the BlockStore cache with env-tunable idle/cleanup settings.
	idleTTL := durationFromEnv(EnvBlockStoreIdleTTL, DefaultBlockStoreIdleTTL)
	cleanupEvery := durationFromEnv(EnvBlockStoreCleanupEvery, DefaultBlockStoreCleanupEvery)
	blockStoreCache = correlate.NewCacheBlockStoreWithConfig(idleTTL, cleanupEvery)
	log.Printf("[Setup] block store cache: idleTTL=%v cleanup=%v (env %s, %s)",
		idleTTL, cleanupEvery, EnvBlockStoreIdleTTL, EnvBlockStoreCleanupEvery)

	// Resolve system (core) cache limits — operator-controlled, not per-processor.
	systemBlockCountSoftLimit = intFromEnv(EnvBlockCountSoftLimit, DefaultBlockCountSoftLimit)
	systemMaxRetention = durationFromEnv(EnvMaxRetention, DefaultMaxRetention)
	log.Printf("[Setup] system cache limits: blockCountSoftLimit=%d maxRetention=%v (env %s, %s)",
		systemBlockCountSoftLimit, systemMaxRetention, EnvBlockCountSoftLimit, EnvMaxRetention)

	// set up listener route such that events can be received from the NATS server and processed
	if subscriberRoute, err = rnats.NewRouteSubscriberUsingSelector(ctx, SelectorSubscriber, MessageCallback); err != nil {
		log.Fatalf("unable to create nats route subscriber: %v", err)
	}

	// setup other require routes, monitor, state sync, state router
	if monitorRoute, err = rnats.NewRouteUsingSelector(ctx, SelectorMonitor); err != nil {
		log.Fatalf("unable to create nats route: %v", err)
	}

	if syncRoute, err = rnats.NewRouteUsingSelector(ctx, SelectorStoreSync); err != nil {
		log.Fatalf("unable to initialize route: %v", err)
	}
}

func Teardown(ctx context.Context) {
	// Shutdown the block store cache
	if blockStoreCache != nil {
		blockStoreCache.Shutdown()
	}

	if backendCache != nil {
		backendCache.Close()
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
