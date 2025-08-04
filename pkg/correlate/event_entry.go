package correlate

import (
	"github.com/quantumwake/alethic-ism-core-go/pkg/data/models"
	"time"
)

// EventEntry wraps an event with its own TTL tracking
type EventEntry struct {
	Data         models.Data
	LastAccessed time.Time
	//TTL          time.Duration
}

// IsExpired checks if the event has expired based on its TTL
//func (e *EventEntry) IsExpired() bool {
//	return time.Now().Sub(e.LastAccessed) > e.TTL
//}

// Touch updates the last accessed time
func (e *EventEntry) Touch() {
	e.LastAccessed = time.Now()
}
