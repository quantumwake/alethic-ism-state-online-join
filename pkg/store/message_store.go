package store

import (
	"container/heap"
	"fmt"
	"time"
)

const (
	X_SECONDS         = 10
	ReconcileInterval = 10 * time.Second
)

type CorrelateData map[string]any

type CorrelateBlock struct {
	data     map[string]*CorrelateDataBlock
	timeHeap *MessageHeap
}

func NewJoinBlock() *CorrelateBlock {
	return &CorrelateBlock{
		data:     make(map[string]*CorrelateDataBlock),
		timeHeap: &MessageHeap{},
	}
}

func (jb *CorrelateBlock) AddJoinBlockData(msg *CorrelateDataBlock) {
	jb.data[msg.Index] = msg
	heap.Push(jb.timeHeap, msg)
}

type CorrelateDataBlock struct {
	Index       string
	SourceData1 *map[string]any
	SourceData2 *map[string]any
	CreatedAt   time.Time
}

func NewMessage(compositeKey string) *CorrelateDataBlock {
	return &CorrelateDataBlock{
		Index:     compositeKey,
		CreatedAt: time.Now(),
	}
}

type MessageHeap []*CorrelateDataBlock

func (h MessageHeap) Len() int           { return len(h) }
func (h MessageHeap) Less(i, j int) bool { return h[i].CreatedAt.Before(h[j].CreatedAt) } // Min heap
func (h MessageHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *MessageHeap) Peek() *CorrelateDataBlock {
	if h.Len() == 0 {
		return nil
	}
	return (*h)[0]
}

func (h *MessageHeap) Push(x interface{}) {
	*h = append(*h, x.(*CorrelateDataBlock))
}

func (h *MessageHeap) Pop() interface{} { // TODO revist this method for potential copy performance between stack and heap
	old := *h         // save the old heap on the stack
	n := len(old)     // get the length of the old heap from the stack
	x := old[n-1]     // get the last element from the old heap
	*h = old[0 : n-1] // remove the last element from the old heap
	return x          // return the last element
}

func (m *CorrelateDataBlock) AddSource(queryState *map[string]interface{}) error {
	//m.jsonMsg = append(m.jsonMsg, msg)
	if m.SourceData1 == nil {
		m.SourceData1 = queryState
		return nil
	} else if m.SourceData2 == nil {
		m.SourceData2 = queryState
		return nil
	}
	return fmt.Errorf("source one and two already set: datablock already exists in store: %v", m)
}

//
//func (m *CorrelateBlock) StoreMessage(key string, queryState *map[string]interface{}) (*CorrelateDataBlock, error) {
//	// check if the message already exists in the store
//	message, ok := m.datablock[key]
//	if !ok {
//		// create a new message and add the source
//		message = NewMessage(key)
//		m.AddBlockData(message)
//		m.datablock[key] = message // pointer to the message
//	}
//
//	err := message.AddSource(queryState)
//	if err != nil {
//		log.Printf("error: %v", err)
//		return nil, fmt.Errorf("error adding source to message: %v", err)
//	}
//	return message, nil
//}
//
//func (m *CorrelateBlock) LoadMessage(key string) (*CorrelateDataBlock, bool) {
//	actual, ok := m.datablock[key]
//	if !ok {
//		return nil, ok
//	}
//	return actual, ok
//}
//
//func (m *CorrelateBlock) RemoveMessage(key string) {
//	message, ok := m.datablock[key]
//	if !ok {
//		return
//	}
//	message.SourceData1 = nil
//	message.SourceData2 = nil
//	delete(m.datablock, key)
//}
//
//func (ms *CorrelateBlock) Reconcile() {
//	now := time.Now()
//	log.Printf("reconciling datablock iteration started: %v\n", now)
//	for ms.timeHeap.Len() > 0 {
//		item := ms.timeHeap.Peek() // Peek at the top item without removing it
//		if now.Sub(item.CreatedAt) > X_SECONDS*time.Second {
//			// TODO debug messaging
//			log.Printf("reconciling message: %v\n given time: %v", item, item.CreatedAt)
//			heap.Pop(ms.timeHeap) // Remove the item from the heap
//			ms.RemoveMessage(item.Index)
//		} else {
//			break
//		}
//	}
//}
//
//func (ms *CorrelateBlock) StartReconcile(ctx context.Context) {
//	ticker := time.NewTicker(ReconcileInterval)
//	defer ticker.Stop()
//
//	for {
//		select {
//		case <-ticker.C:
//			ms.Reconcile()
//		case <-ctx.Done():
//			log.Println("Stopping reconcile loop")
//			//close(ms.reconcileDone)
//			return
//		}
//	}
//}
