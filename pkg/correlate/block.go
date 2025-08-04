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

// Block is the upper-level structure that defines key fields,
// holds data blocks, and performs join and eviction.
type Block struct {
	KeyDefinitions state.ColumnKeyDefinitions // fields defining the correlation key

	// stop watch for measuring performance of the block
	Statistics *Statistics

	// dataBlocks provides fast lookup by key.
	dataBlocks map[string]*DataBlock
	// heap orders the data blocks by evictionTime.
	heap DataBlockHeap

	mu sync.Mutex

	// Configuration thresholds:
	softMaxThreshold int           // if total blocks exceed this, soft eviction is applied
	eventTTL         time.Duration // TTL for individual events
	softEvictWindow  time.Duration // each event resets evictionTime to now + softEvictWindow
	hardEvictWindow  time.Duration // absolute lifetime of a data block
}

// NewBlock creates a new Block.
func NewBlock(keyDefinitions state.ColumnKeyDefinitions, softMaxThreshold int, softWindow, hardWindow time.Duration) *Block {
	cb := &Block{
		KeyDefinitions:   keyDefinitions,
		dataBlocks:       make(map[string]*DataBlock),
		heap:             DataBlockHeap{},
		softMaxThreshold: softMaxThreshold,
		eventTTL:         softWindow, // Use softWindow as event TTL
		softEvictWindow:  softWindow,
		hardEvictWindow:  hardWindow,
		Statistics:       NewStopWatch().Start(),
	}
	heap.Init(&cb.heap)
	go cb.evictionLoop()
	return cb
}

// getKey builds a unique key for an event based on the block's KeyDefinitions.
func (cb *Block) getKey(event models.Data) (string, error) {
	key := ""
	for _, field := range cb.KeyDefinitions {
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
func joinData(src1 string, e1 models.Data, src2 string, e2 models.Data, keyDefinitions state.ColumnKeyDefinitions) models.Data {
	result := make(models.Data)
	// Copy key fields from one event (assumed identical in both)
	for _, field := range keyDefinitions {
		if v, ok := e1[field.Name]; ok {
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
	addFields(src1, e1)
	addFields(src2, e2)
	result["joinedAt"] = time.Now().Format(time.RFC3339)
	return result
}

// AddData processes an incoming event from a given source. It only joins events from different sources.
func (cb *Block) AddData(source string, data models.Data, callback func(data models.Data) error) error {
	stopWatch := NewStopWatch().Start()
	defer func() { // calculate execution time and add lab statistics
		elapsed := stopWatch.Stop().Elapsed()
		cb.Statistics.LapWith(elapsed)
	}()

	cb.mu.Lock()
	defer cb.mu.Unlock()

	key, err := cb.getKey(data)
	if err != nil {
		return fmt.Errorf("could not get key for data %v: %v", data, err)
	}
	now := time.Now()
	db, exists := cb.dataBlocks[key]
	if !exists {
		// Create a new data block.
		db = &DataBlock{
			key:          key,
			dataBySource: make(DataBySource),
			lastUpdated:  now,
			evictionTime: now.Add(cb.softEvictWindow),
			eventTTL:     cb.eventTTL,
			count:        0,
		}
		cb.dataBlocks[key] = db
		heap.Push(&cb.heap, db)
	}

	// For each event already in the block from a different source, perform a join.
	for otherSource, events := range db.dataBySource {
		// Reset TTL for existing events, irrespective of its source
		// As long as there is new data coming in on the join key value,
		// we keep all data -- only when there is no longer any data on the
		// same join key value do we evict the events for this join-key value
		for _, event := range events {
			event.Touch()
		}

		if otherSource == source {
			continue // do not join events from the same source
		}

		//avg := utils.FormatNanoSeconds(cb.Statistics.Avg())
		avg := time.Duration(cb.Statistics.Avg()).Seconds()

		for _, eventEntry := range events {
			// Skip expired events
			if now.Sub(eventEntry.LastAccessed) > cb.eventTTL {
				continue
			}
			joinResult := joinData(otherSource, eventEntry.Data, source, data, cb.KeyDefinitions)
			log.Printf("\t%.10f\t%+v\n", avg, joinResult)
			if err = callback(joinResult); err != nil {
				return fmt.Errorf("could not process data: %v", err)
			}
		}
	}

	// Create new event entry and add to the block
	eventEntry := &EventEntry{
		Data:         data,
		LastAccessed: now,
	}
	db.dataBySource[source] = append(db.dataBySource[source], eventEntry)
	db.count++
	db.lastUpdated = now

	// Always reset the block eviction time on each new event (from any source)
	db.evictionTime = now.Add(cb.softEvictWindow)
	heap.Fix(&cb.heap, db.index)
	return nil
}

// evictionLoop runs periodically to remove stale data blocks.
// If the total number of data blocks exceeds the soft threshold, blocks whose evictionTime has passed are evicted.
// Additionally, blocks that have exceeded the hardEvictWindow are removed.
func (cb *Block) evictionLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	evictFn := func() {
		now := time.Now()

		// acquire the lock and check for stale data blocks to evict
		cb.mu.Lock()
		defer cb.mu.Unlock()

		// Hard eviction: remove blocks older than hardEvictWindow.
		for cb.heap.Len() > 0 {
			db := cb.heap[0] // peek the oldest block

			// if the block is younger than hardEvictWindow, stop hard eviction
			if db.lastUpdated.Add(cb.hardEvictWindow).After(now) {
				break
			}

			heap.Pop(&cb.heap)            // remove the block from the heap
			delete(cb.dataBlocks, db.key) // remove the block from the map
			log.Printf("Hard evicting data block with key %s", db.key)
		}

		// iterate over the heap
		for cb.heap.Len() > 0 {

			// SoftMaxThreshold: if total number of data blocks not exceeds softMaxThreshold, do not evict any blocks
			if len(cb.dataBlocks) <= cb.softMaxThreshold {
				// the heap will continue to grow until the hard eviction removes the block, OR
				// the total number of data blocks is greater than softMaxThreshold and the soft
				// eviction time of the data block has passed
				return
			}

			// Soft eviction: remove blocks whose evictionTime has passed.
			db := cb.heap[0]
			if db.evictionTime.Before(now) {
				heap.Pop(&cb.heap)
				delete(cb.dataBlocks, db.key)
				log.Printf("Soft evicting data block with key %s", db.key)
			} else {
				break
			}

		}
	}

	// listen on the ticker channel
	for range ticker.C {
		evictFn()
	}
}
