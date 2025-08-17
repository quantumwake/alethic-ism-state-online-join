package correlate

import (
	"github.com/quantumwake/alethic-ism-core-go/pkg/data/models"
	"time"
)

// BlockPart wraps a data item with its own TTL tracking and join count
type BlockPart struct {
	Data      models.Data
	ExpireAt  time.Time
	JoinCount int
}
