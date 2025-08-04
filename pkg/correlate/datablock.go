package correlate

import (
	"time"
)

// Data represents an incoming JSON event.
//type Data map[string]interface{}

type DataBySource map[string][]*EventEntry

// DataBlock holds events joined by a common key, stored by source.
type DataBlock struct {
	key          string
	dataBySource DataBySource
	//dataBySource map[string][]models.Data // events organized by their source
	lastUpdated  time.Time // last update time
	evictionTime time.Time // updated on every event arrival
	eventTTL     time.Duration
	count        int // total events in this block
	index        int // index in the heap
}

// DataBlockHeap implements a min-heap sorted by evictionTime.
type DataBlockHeap []*DataBlock

func (h DataBlockHeap) Len() int { return len(h) }
func (h DataBlockHeap) Less(i, j int) bool {
	return h[i].evictionTime.Before(h[j].evictionTime)
}
func (h DataBlockHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}
func (h *DataBlockHeap) Push(x interface{}) {
	n := len(*h)
	item := x.(*DataBlock)
	item.index = n
	*h = append(*h, item)
}

func (h *DataBlockHeap) Pop() interface{} {
	old := *h         // save the old heap on the stack
	n := len(old)     // get the length of the old heap from the stack
	x := old[n-1]     // get the last element from the old heap
	x.index = -1      // for safety
	*h = old[0 : n-1] // remove the last element from the old heap
	return x          // return the last element
}
