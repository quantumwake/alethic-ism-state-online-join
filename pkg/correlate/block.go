package correlate

import (
	"container/heap"
	"fmt"
	"github.com/quantumwake/alethic-ism-core-go/pkg/data/models"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/state"
	"log"
	"sync"
	"time"
)

// BlockStore is the upper-level structure that defines key fields,
// holds blocks, and performs join and eviction of entire blocks and individual parts
type BlockStore struct {
	JoinKeyDefinitions state.ColumnKeyDefinitions // fields defining the correlation key

	// stop watch for measuring performance of the store
	Statistics *Statistics

	// blocks provides fast lookup by join key.
	blocks map[string]*Block
	// heap orders the blocks by evictionTime.
	heap blockHeap

	mu sync.Mutex

	// Block management configuration
	blockCountSoftLimit int           // if total blocks exceed this, eviction window will apply
	blockWindowTTL      time.Duration // sliding window TTL for a block, resets on each new event

	// BlockPart management configuration
	blockPartMaxJoinCount int           // hard limit on how many times a part can be joined
	blockPartMaxAge       time.Duration // absolute lifetime of a part from creation

	// Lifecycle management
	shutdownCh   chan struct{}
	lastAccessed time.Time
}

type KeyedBlock map[string]*Block

// NewBlockStore creates a new BlockStore.
func NewBlockStore(keyDefinitions state.ColumnKeyDefinitions, blockCountSoftLimit, blockPartMaxJoinCount int, blockWindowTTL, blockPartMaxAge time.Duration) *BlockStore {
	store := &BlockStore{
		JoinKeyDefinitions:    keyDefinitions,
		blocks:                make(KeyedBlock),
		heap:                  blockHeap{},
		blockCountSoftLimit:   blockCountSoftLimit,
		blockPartMaxJoinCount: blockPartMaxJoinCount,
		blockWindowTTL:        blockWindowTTL,
		blockPartMaxAge:       blockPartMaxAge,
		Statistics:            NewStopWatch().Start(),
		shutdownCh:            make(chan struct{}),
		lastAccessed:          time.Now(),
	}
	
	log.Print(LogBlockStoreCreated(keyDefinitions, blockCountSoftLimit, blockPartMaxJoinCount, blockWindowTTL, blockPartMaxAge))
	
	heap.Init(&store.heap)
	go store.evictionLoop()
	return store
}

// GetJoinKeyValue builds a unique key for an event based on the store's JoinKeyDefinitions.
func (store *BlockStore) GetJoinKeyValue(event models.Data) (string, error) {
	key := ""
	for _, field := range store.JoinKeyDefinitions {
		value, ok := event[field.Name]
		if !ok {
			return "", fmt.Errorf("field `%s` not present in event", field.Name)
		}
		key += fmt.Sprintf("%v|", value)
	}
	return key, nil
}

// joinData joins two events from different sources.
// The key fields are copied as-is, and non-key fields are prefixed with the source identifier.
func joinData(src1 string, e1 *BlockPart, src2 string, e2 *BlockPart, keyDefinitions state.ColumnKeyDefinitions) models.Data {
	result := make(models.Data)
	// Copy key fields from one event (assumed identical in both)
	for _, field := range keyDefinitions {
		if v, ok := e1.Data[field.Name]; ok {
			result[field.Name] = v
		}
	}

	// Helper to add non-key fields with source prefix.
	addFields := func(src string, e models.Data) {
		for k, v := range e {
			// Skip key fields.
			skip := false
			for _, field := range keyDefinitions {
				if k == field.Name {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
			//result[fmt.Sprintf("%s_%s", src, k)] = v

			// TODO this is where the new field is created that isn't a key, we need a better way to derive the fields, maybe use the remap definitions?
			//  ALTERNATIVELY: we can do same_1 same_2 if there are duplicates?
			result[fmt.Sprintf("%s", k)] = v

		}
	}

	addFields(src1, e1.Data)
	addFields(src2, e2.Data)
	result["joinedAt"] = time.Now().Format(time.RFC3339)
	e1.JoinCount++ // increase the number of times this data was joined
	e2.JoinCount++ // increase the number of times this data was joined
	return result
}

func (store *BlockStore) EvictExpiredBlocks() {
	// If concurrent access is possible:
	// store.mu.Lock()
	// defer store.mu.Unlock()

	now := time.Now()
	for store.heap.Len() > 0 {
		// assuming store.heap is a heap over items with evictionTime
		// and heapIndex access to the underlying slice is valid here.
		if store.heap[0].evictionTime.After(now) {
			break
		}
		heap.Pop(&store.heap) // discard returned item
	}
}

func (store *BlockStore) GetOrAddBlock(joinKeyValue string) (*Block, error) {
	//store.mu.Lock()
	//defer store.mu.Unlock()

	if block, ok := store.blocks[joinKeyValue]; ok {
		return block, nil
	}

	now := time.Now()
	block := &Block{
		key:           joinKeyValue,
		partsBySource: make(PartsBySource),
		evictionTime:  now.Add(store.blockWindowTTL),
		heapIndex:     -1,
	}

	store.blocks[joinKeyValue] = block
	heap.Push(&store.heap, block)
	
	log.Print(LogNewBlockCreated(joinKeyValue, block.evictionTime, store.blockWindowTTL, 
		len(store.blocks), store.blockCountSoftLimit))
	
	return block, nil
}

// AddData processes an incoming event from a given source. It only joins events from different sources.
func (store *BlockStore) AddData(inboundSourceID string, inboundSourceData models.Data, callback func(data models.Data) error) error {
	stopWatch := NewStopWatch().Start()
	defer func() { // calculate execution time and add lab statistics
		elapsed := stopWatch.Stop().Elapsed()
		store.Statistics.LapWith(elapsed)
	}()

	store.mu.Lock()
	defer store.mu.Unlock()

	// Update last accessed time
	store.lastAccessed = time.Now()

	// given a block (e.g. joinKeyValue-value map, get the value of the joinKeyValue, as defined by this BlockStore)
	joinKeyValue, err := store.GetJoinKeyValue(inboundSourceData)
	if err != nil {
		return fmt.Errorf("could not get joinKeyValue for source data %v: %v", inboundSourceData, err)
	}

	now := time.Now() // stamps

	// Within the store, we maintain a map of blocks - the key is derived from the join key definition.
	// NOTE: THIS IS AN UNSAFE OPERATION, store.mu.Lock will sync this, however.
	block, _ := store.GetOrAddBlock(joinKeyValue)

	// we track the inbound data by wrapping it in a block part
	inboundSourcePart := &BlockPart{
		Data:      inboundSourceData,
		ExpireAt:  now.Add(store.blockPartMaxAge),
		JoinCount: 0,
	}

	// store the new inbound part
	block.partsBySource[inboundSourceID] = append(block.partsBySource[inboundSourceID], inboundSourcePart)
	
	// Log the new part addition
	existingParts := len(block.partsBySource[inboundSourceID]) - 1
	totalSourcesInBlock := len(block.partsBySource)
	log.Print(LogNewPartAdded(joinKeyValue, inboundSourceID, existingParts, totalSourcesInBlock,
		inboundSourcePart.ExpireAt, store.blockPartMaxAge))

	// under the block, we separate out the arrival data by source
	// this allows us to join the received data (on source) against all other sources
	for storedSourceID, storedParts := range block.partsBySource {
		if inboundSourceID == storedSourceID {
			continue // do not join events from the same source
		}

		// avg is calculated inside LogJoinOperation function
		// avg := time.Duration(store.Statistics.Avg()).Seconds()

		write := 0                               // in-place compaction position
		skippedExpired := 0
		skippedMaxJoins := 0
		for _, storedPart := range storedParts { /// iterate each stored part as per previously stored source
			// Check if the part is expired (ExpireAt is in the past)
			expired := storedPart.ExpireAt.Before(now)

			// Check if the part has reached max join count
			maxJoinsReached := storedPart.JoinCount >= store.blockPartMaxJoinCount

			// Skip this part if either condition is true
			if expired || maxJoinsReached {
				if expired {
					skippedExpired++
				}
				if maxJoinsReached {
					skippedMaxJoins++
				}
				continue // note, we do not move write heapIndex forward
			}

			// if the part is not expired, we need to first make sure we keep it, and then we join it.
			storedParts[write] = storedPart // in-place overwrite, (e.g. this essentially keeps all stored values at the top of the slice)

			// we join it after we have determined to keep this part.
			joinResult := joinData(
				storedSourceID,
				storedPart,
				inboundSourceID,
				inboundSourcePart,
				store.JoinKeyDefinitions)

			log.Print(LogJoinOperation(joinKeyValue, store.JoinKeyDefinitions, joinResult,
				storedSourceID, inboundSourceID, storedPart, inboundSourcePart,
				store.blockPartMaxJoinCount, time.Duration(store.Statistics.Avg())))
			if err = callback(joinResult); err != nil {
				return fmt.Errorf("could not process availablePart: %v", err)
			}

			write++ // move write heapIndex forward
		}

		// Log if parts were skipped
		if skippedExpired > 0 || skippedMaxJoins > 0 {
			log.Print(LogPartSkipped(joinKeyValue, storedSourceID, skippedExpired, skippedMaxJoins, 
				write, store.blockPartMaxAge, store.blockPartMaxJoinCount))
		}

		// now that we have iterated the storedParts and completed any relevant join, we need to compact the list, if any
		// we do this by essentially taking nilling out any pointers below the write heapIndex of the slice (remember we moved all kept objects up)
		for index := write; index < len(storedParts); index++ {
			storedParts[index] = nil // this is such that the GC can finalize these objects
		}
		block.partsBySource[storedSourceID] = storedParts[:write] // we then need to shrink the slice appropriately so len == write heapIndex (exclusively)
	}

	// Always reset the block eviction time on each new event (sliding window)
	block.evictionTime = now.Add(store.blockWindowTTL)
	heap.Fix(&store.heap, block.heapIndex)
	return nil
}

// Shutdown stops the eviction loop and cleans up resources
func (store *BlockStore) Shutdown() {
	store.mu.Lock()
	blockCount := len(store.blocks)
	totalParts := 0
	totalSources := 0
	sourceMap := make(map[string]int)
	
	for _, block := range store.blocks {
		for sourceID, parts := range block.partsBySource {
			totalParts += len(parts)
			sourceMap[sourceID] += len(parts)
		}
	}
	totalSources = len(sourceMap)
	store.mu.Unlock()
	
	log.Print(LogBlockStoreShutdown(store.JoinKeyDefinitions, blockCount, totalParts, totalSources, store.Statistics))
	close(store.shutdownCh)
}

// IsIdle returns true if the store hasn't been accessed for longer than the idle duration
func (store *BlockStore) IsIdle(idleDuration time.Duration) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	return time.Since(store.lastAccessed) > idleDuration
}

// evictionLoop runs periodically to remove stale blocks.
// If the total number of blocks exceeds the soft threshold, blocks whose evictionTime has passed are evicted.
func (store *BlockStore) evictionLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	evictFn := func() {
		now := time.Now()

		// acquire the lock and check for stale blocks to evict
		store.mu.Lock()
		defer store.mu.Unlock()

		//// Hard eviction: remove blocks older than hardEvictWindow.
		//for block.heap.Len() > 0 {
		//	db := block.heap[0] // peek the oldest block
		//
		//	// if the block is younger than hardEvictWindow, stop hard eviction
		//	if db.lastUpdated.Add(block.hardEvictWindow).After(now) {
		//		break
		//	}
		//
		//	heap.Pop(&block.heap)            // remove the block from the heap
		//	delete(block.dataBlocks, db.key) // remove the block from the map
		//	log.Printf("Hard evicting data block with key %s", db.key)
		//}

		// iterate over the heap
		for store.heap.Len() > 0 {

			// SoftMaxThreshold: if total number of blocks not exceeds blockCountSoftLimit, do not evict any blocks
			if len(store.blocks) <= store.blockCountSoftLimit {
				// the heap will continue to grow until the hard eviction removes the block, OR
				// the total number of blocks is greater than blockCountSoftLimit and the soft
				// eviction time of the block has passed
				return
			}

			// Soft eviction: remove blocks whose evictionTime has passed.
			blk := store.heap[0]
			if blk.evictionTime.Before(now) {
				heap.Pop(&store.heap)
				delete(store.blocks, blk.key)
				
				log.Print(LogBlockEviction("BlockStore", blk, store.JoinKeyDefinitions, 
					len(store.blocks), store.blockCountSoftLimit))
			} else {
				break
			}

		}
	}

	// listen on the ticker channel or shutdown signal
	for {
		select {
		case <-ticker.C:
			evictFn()
		case <-store.shutdownCh:
			return
		}
	}
}
