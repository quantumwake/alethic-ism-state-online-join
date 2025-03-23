package correlate_test

import (
	"alethic-ism-state-online-join/pkg/correlate"
	"fmt"
	"github.com/quantumwake/alethic-ism-core-go/pkg/data/models"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/state"
	"github.com/stretchr/testify/require"
	"log"
	"math/rand"
	"testing"
	"time"
)

func TestBlock_AddData(t *testing.T) {
	source1 := []models.Data{
		{
			"category":              "class_a",
			"framework":             "Utilitarian",
			"framework_definitions": "some definitions for utilitarian",
		},
		{
			"category":              "class_b",
			"framework":             "Care Ethicist",
			"framework_definitions": "some definitions for care ethicist",
		},
	}

	source2 := []models.Data{
		{
			"id":        "ABC_01",
			"category":  "class_a",
			"framework": "Utilitarian",
			"scenario":  "1. some specific scenario that needs to be matched against utilitarian",
		},
		{
			"id":        "ABC_02",
			"category":  "class_a",
			"framework": "Utilitarian",
			"scenario":  "2. some specific scenario that needs to be matched against utilitarian",
		},
		{
			"id":        "ABC_03",
			"category":  "class_b",
			"framework": "Care Ethicist",
			"scenario":  "3. some specific scenario that needs to be matched against care ethicist",
		},

		//
		{
			"id":        "DEF_01",
			"category":  "class_b",
			"framework": "Care Ethicist",
			"scenario":  "1. some specific scenario that needs to be matched against care ethicist",
		},
		{
			"id":        "DEF_02",
			"category":  "class_b",
			"framework": "Care Ethicist",
			"scenario":  "2. some specific scenario that needs to be matched against care ethicist",
		},
		{
			"id":        "DEF_03",
			"category":  "class_a",
			"framework": "Utilitarian",
			"scenario":  "3. some specific scenario that needs to be matched against utilitarian",
		},
	}

	keys := state.ColumnKeyDefinitions{
		{Name: "framework"},
		{Name: "category"},
	}

	block := correlate.NewBlock(
		keys,
		10,
		10*time.Second,
		30*time.Second)

	require.NoError(t, block.AddData("source1", source1[0], func(data models.Data) error {
		return fmt.Errorf("should not happen")
	}))

	require.NoError(t, block.AddData("source1", source1[1], func(data models.Data) error {
		return fmt.Errorf("should not happen")
	}))

	require.NoError(t, block.AddData("source2", source2[0], func(data models.Data) error {
		fmt.Printf("%+v\n", data)
		require.Equal(t, data["category"], "class_a")
		require.Equal(t, data["framework"], "Utilitarian")
		require.Equal(t, data["id"], "ABC_01")
		return nil
	}))

	require.NoError(t, block.AddData("source2", source2[1], func(data models.Data) error {
		fmt.Printf("%+v\n", data)
		require.Equal(t, data["category"], "class_a")
		require.Equal(t, data["framework"], "Utilitarian")
		require.Equal(t, data["id"], "ABC_02")
		return nil
	}))

	require.NoError(t, block.AddData("source2", source2[2], func(data models.Data) error {
		fmt.Printf("%+v\n", data)
		require.Equal(t, data["category"], "class_b")
		require.Equal(t, data["framework"], "Care Ethicist")
		require.Equal(t, data["id"], "ABC_03")
		return nil
	}))

	require.NoError(t, block.AddData("source2", source2[3], func(data models.Data) error {
		fmt.Printf("%+v\n", data)
		require.Equal(t, data["category"], "class_b")
		require.Equal(t, data["framework"], "Care Ethicist")
		require.Equal(t, data["id"], "DEF_01")
		return nil
	}))

	require.NoError(t, block.AddData("source2", source2[4], func(data models.Data) error {
		fmt.Printf("%+v\n", data)
		require.Equal(t, data["category"], "class_b")
		require.Equal(t, data["framework"], "Care Ethicist")
		require.Equal(t, data["id"], "DEF_02")
		return nil
	}))

	require.NoError(t, block.AddData("source2", source2[5], func(data models.Data) error {
		fmt.Printf("%+v\n", data)
		require.Equal(t, data["category"], "class_a")
		require.Equal(t, data["framework"], "Utilitarian")
		require.Equal(t, data["id"], "DEF_03")
		return nil
	}))
}

func TestBlock(t *testing.T) {
	// Use "id" as the correlation key field.
	softMaxThreshold := 10
	softWindow := 10 * time.Second
	hardWindow := 30 * time.Second

	keys := state.ColumnKeyDefinitions{
		{Name: "id"},
	}

	block := correlate.NewBlock(
		keys,
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

			//
			require.NoError(t, block.AddData(source, data, func(data models.Data) error {
				log.Printf("%+v", data)
				return nil
			}))

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
