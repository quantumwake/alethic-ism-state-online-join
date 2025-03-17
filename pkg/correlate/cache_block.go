package correlate

import "sync"

type CacheBlock struct {
	blockMap map[string]*Block
	mu       sync.RWMutex
}

func NewCacheBlock() *CacheBlock {
	return &CacheBlock{
		blockMap: make(map[string]*Block),
	}
}

func (cb *CacheBlock) Get(id string) *Block {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.blockMap[id]
}

func (cb *CacheBlock) GetOrSet(id string, fn func() *Block) *Block {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if _, ok := cb.blockMap[id]; ok {
		return cb.blockMap[id]
	}
	block := fn()
	cb.blockMap[id] = block
	return block
}

func (cb *CacheBlock) Exists(id string) bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	_, ok := cb.blockMap[id]
	return ok
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
