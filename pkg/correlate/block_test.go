package correlate_test

import (
	"alethic-ism-transformer-state-consolidator/pkg/correlate"
	"fmt"
	"github.com/quantumwake/alethic-ism-core-go/pkg/data/models"
	"math/rand"
	"testing"
	"time"
)

func TestBlock(t *testing.T) {
	// Use "id" as the correlation key field.
	keyFields := []string{"id"}
	softMaxThreshold := 10
	softWindow := 10 * time.Second
	hardWindow := 30 * time.Second

	block := correlate.NewBlock(
		keyFields,
		softMaxThreshold,
		softWindow,
		hardWindow)

	dataAppender := func(source string, maxCount int) {
		minTick := 1 * time.Millisecond
		maxTick := 5 * time.Millisecond

		// Start with a random duration
		ticker := time.NewTicker(minTick)
		defer ticker.Stop()

		for {
			<-ticker.C
			data := models.Data{
				"id":                           maxCount,
				fmt.Sprintf("value%s", source): fmt.Sprintf("B%d", maxCount),
				fmt.Sprintf("extra%s", source): fmt.Sprintf("E%d", maxCount),
			}
			block.AddData(source, data)
			ticker.Stop()
			newTick := minTick + time.Duration(rand.Int63n(int64(maxTick-minTick)))
			//seconds := newTick.Seconds()
			//fmt.Printf("%d\t%v\n", newTick, seconds)

			ticker = time.NewTicker(newTick)

			maxCount--
			if maxCount == 0 {
				break
			}
		}
	}

	go dataAppender("1", 10000000)
	go dataAppender("2", 10000000)

	// Wait for the data to be processed.
	time.Sleep(120 * time.Second)

	avg := block.Statistics.Avg()
	fmt.Printf("Average: %d\n", avg)
}
