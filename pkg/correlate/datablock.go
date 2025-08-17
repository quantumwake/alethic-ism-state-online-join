package correlate

import (
	"time"
)

// Data represents an incoming JSON event.
//type Data map[string]interface{}

type PartsBySource map[string][]*BlockPart

// Block holds events joined by a common key, stored by source.
type Block struct {
	key           string
	partsBySource PartsBySource
	evictionTime  time.Time // updated on every event arrival (sliding window)
	heapIndex     int       // heapIndex in the heap
}

// blockHeap implements a min-heap sorted by evictionTime.
type blockHeap []*Block

func (h blockHeap) Len() int { return len(h) }
func (h blockHeap) Less(i, j int) bool {
	return h[i].evictionTime.Before(h[j].evictionTime)
}
func (h blockHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}
func (h *blockHeap) Push(x interface{}) {
	n := len(*h)
	item := x.(*Block)
	item.heapIndex = n
	*h = append(*h, item)
}

func (h *blockHeap) Pop() interface{} {
	old := *h         // save the old heap on the stack
	n := len(old)     // get the length of the old heap from the stack
	x := old[n-1]     // get the last element from the old heap
	x.heapIndex = -1  // for safety
	*h = old[0 : n-1] // remove the last element from the old heap
	return x          // return the last element
}
