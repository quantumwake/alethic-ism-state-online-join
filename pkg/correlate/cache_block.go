package correlate

import (
	"log"
	"sync"
	"time"
)

type CacheBlockStore struct {
	storeMap        map[string]*BlockStore
	mu              sync.RWMutex
	shutdownCh      chan struct{}
	storeIdleTTL    time.Duration // How long a store can be idle before cleanup
	cleanupInterval time.Duration // How often to check for idle stores
}

func NewCacheBlockStore() *CacheBlockStore {
	// Default: clean up stores idle for 2 hours, check every 5 minutes
	return NewCacheBlockStoreWithConfig(2*time.Hour, 5*time.Minute)
}

// NewCacheBlockStoreWithConfig creates a cache with custom idle timeout and cleanup interval
func NewCacheBlockStoreWithConfig(storeIdleTTL, cleanupInterval time.Duration) *CacheBlockStore {
	cbs := &CacheBlockStore{
		storeMap:        make(map[string]*BlockStore),
		shutdownCh:      make(chan struct{}),
		storeIdleTTL:    storeIdleTTL,
		cleanupInterval: cleanupInterval,
	}
	go cbs.cleanupLoop()
	return cbs
}

func (cbs *CacheBlockStore) Get(id string) *BlockStore {
	cbs.mu.RLock()
	defer cbs.mu.RUnlock()
	return cbs.storeMap[id]
}

func (cbs *CacheBlockStore) GetOrSet(id string, fn func() (*BlockStore, error)) (*BlockStore, error) {
	cbs.mu.Lock()
	defer cbs.mu.Unlock()
	if _, ok := cbs.storeMap[id]; ok {
		return cbs.storeMap[id], nil
	}
	store, err := fn()
	if err != nil {
		return nil, err
	}
	cbs.storeMap[id] = store
	return store, nil
}

func (cbs *CacheBlockStore) Exists(id string) bool {
	cbs.mu.RLock()
	defer cbs.mu.RUnlock()
	_, ok := cbs.storeMap[id]
	return ok
}

// Remove removes a BlockStore and shuts it down
func (cbs *CacheBlockStore) Remove(id string) {
	cbs.mu.Lock()
	defer cbs.mu.Unlock()
	if store, ok := cbs.storeMap[id]; ok {
		log.Printf("[CacheBlockStore] Removing BlockStore for route: %s", id)
		store.Shutdown()
		delete(cbs.storeMap, id)
	}
}

// Shutdown stops all BlockStores and the cleanup loop
func (cbs *CacheBlockStore) Shutdown() {
	log.Printf("[CacheBlockStore] Shutting down cache with %d active stores", len(cbs.storeMap))
	close(cbs.shutdownCh)
	
	cbs.mu.Lock()
	defer cbs.mu.Unlock()
	for id, store := range cbs.storeMap {
		log.Printf("[CacheBlockStore] Shutting down BlockStore for route: %s", id)
		store.Shutdown()
	}
	cbs.storeMap = make(map[string]*BlockStore)
}

// cleanupLoop periodically checks for idle BlockStores and removes them
func (cbs *CacheBlockStore) cleanupLoop() {
	ticker := time.NewTicker(cbs.cleanupInterval)
	defer ticker.Stop()
	
	log.Printf("[CacheBlockStore] Started cleanup loop (check every %v, idle threshold: %v)", 
		cbs.cleanupInterval, cbs.storeIdleTTL)
	
	for {
		select {
		case <-ticker.C:
			cbs.cleanupIdleStores()
		case <-cbs.shutdownCh:
			log.Println("[CacheBlockStore] Cleanup loop shutting down")
			return
		}
	}
}

// cleanupIdleStores removes BlockStores that have been idle for longer than storeIdleTTL
func (cbs *CacheBlockStore) cleanupIdleStores() {
	cbs.mu.Lock()
	defer cbs.mu.Unlock()
	
	log.Printf("[CacheBlockStore] Running cleanup check on %d stores (idle threshold: %v)", 
		len(cbs.storeMap), cbs.storeIdleTTL)
	
	var toRemove []string
	now := time.Now()
	for id, store := range cbs.storeMap {
		if store.IsIdle(cbs.storeIdleTTL) {
			toRemove = append(toRemove, id)
		}
	}
	
	if len(toRemove) > 0 {
		log.Printf("[CacheBlockStore] Found %d idle stores to remove", len(toRemove))
	}
	
	for _, id := range toRemove {
		if store, ok := cbs.storeMap[id]; ok {
			store.mu.Lock()
			idleTime := now.Sub(store.lastAccessed)
			blockCount := len(store.blocks)
			store.mu.Unlock()
			
			log.Printf("[CacheBlockStore] Removing idle BlockStore for route: %s (idle: %v, blocks: %d)", 
				id, idleTime, blockCount)
			store.Shutdown()
			delete(cbs.storeMap, id)
		}
	}
	
	if len(toRemove) > 0 {
		log.Printf("[CacheBlockStore] Cleanup complete. Active stores: %d", len(cbs.storeMap))
	}
}

//
//func (cb *CacheBlock) GetOrSetBlock(id string, block *correlate.Block) *correlate.Block {
//	cb.mu.Lock()
//	defer cb.mu.Unlock()
//	if _, ok := cb.blockMap[id]; !ok {
//		cb.blockMap[id] = block
//	}
//	return cb.blockMap[id]
//}
