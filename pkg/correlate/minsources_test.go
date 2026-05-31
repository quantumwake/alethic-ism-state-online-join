package correlate_test

import (
	"testing"
	"time"

	"alethic-ism-state-online-join/pkg/correlate"
	"github.com/quantumwake/alethic-ism-core-go/pkg/data/models"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/state"
	"github.com/stretchr/testify/require"
)

// TestBlockStore_MinSources_GateThree verifies that MinSources gates emission until the
// required number of distinct sources have contributed to a key.
//
// MinSources=3, arrivals a1, b1 (2 sources) -> nothing emitted; c1 (3rd source) opens the
// gate and C joins A and B pairwise -> 2 rows.
func TestBlockStore_MinSources_GateThree(t *testing.T) {
	keys := state.ColumnKeyDefinitions{{Name: "k"}}
	store := correlate.NewBlockStore(keys, 10)
	ccc := correlate.CacheControlContext{
		BlockPartMaxJoinCount: 0, // unlimited, so the gate is what's under test
		BlockWindowTTL:        time.Minute,
		BlockPartMaxAge:       time.Minute,
		MinSources:            3,
	}

	emits := 0
	collect := func(models.Data) error { emits++; return nil }

	require.NoError(t, store.AddData(ccc, "A", models.Data{"k": "K", "a": "a1"}, collect))
	require.NoError(t, store.AddData(ccc, "B", models.Data{"k": "K", "b": "b1"}, collect))
	require.Equal(t, 0, emits, "with MinSources=3, two sources must not emit yet")

	require.NoError(t, store.AddData(ccc, "C", models.Data{"k": "K", "c": "c1"}, collect))
	require.Equal(t, 2, emits, "third source opens the gate: C joins A and B (pairwise) -> 2 rows")
}

// TestBlockStore_MinSources_DefaultTwo verifies the floor/default of 2: a second source
// opens the gate and the pair is emitted.
func TestBlockStore_MinSources_DefaultTwo(t *testing.T) {
	keys := state.ColumnKeyDefinitions{{Name: "k"}}
	store := correlate.NewBlockStore(keys, 10)
	ccc := correlate.CacheControlContext{
		BlockPartMaxJoinCount: 0,
		BlockWindowTTL:        time.Minute,
		BlockPartMaxAge:       time.Minute,
		MinSources:            2,
	}

	emits := 0
	collect := func(models.Data) error { emits++; return nil }

	require.NoError(t, store.AddData(ccc, "A", models.Data{"k": "K", "a": "a1"}, collect))
	require.Equal(t, 0, emits, "a single source cannot join")
	require.NoError(t, store.AddData(ccc, "B", models.Data{"k": "K", "b": "b1"}, collect))
	require.Equal(t, 1, emits, "MinSources=2: the second source emits the pair")
}

// TestBlockStore_MultiSourcePairwise documents that with 3 sources and no gate
// (MinSources=0) the join is pairwise — each new source joins all other sources, one row
// per pair (no single combined A+B+C tuple).
func TestBlockStore_MultiSourcePairwise(t *testing.T) {
	keys := state.ColumnKeyDefinitions{{Name: "k"}}
	store := correlate.NewBlockStore(keys, 10)
	ccc := correlate.CacheControlContext{
		BlockPartMaxJoinCount: 0, // unlimited (per-part budget is shared across all sources)
		BlockWindowTTL:        time.Minute,
		BlockPartMaxAge:       time.Minute,
		MinSources:            0, // gate disabled
	}

	emits := 0
	collect := func(models.Data) error { emits++; return nil }

	require.NoError(t, store.AddData(ccc, "A", models.Data{"k": "K", "a": "a1"}, collect))
	require.NoError(t, store.AddData(ccc, "B", models.Data{"k": "K", "b": "b1"}, collect)) // a1·b1
	require.NoError(t, store.AddData(ccc, "C", models.Data{"k": "K", "c": "c1"}, collect)) // c1·a1, c1·b1
	require.Equal(t, 3, emits, "pairwise across 3 sources: a·b, then c·a and c·b")
}
