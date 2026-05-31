package correlate_test

import (
	"alethic-ism-state-online-join/pkg/correlate"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/quantumwake/alethic-ism-core-go/pkg/data/models"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/state"
	"github.com/stretchr/testify/require"
)

// TestBlockStore_NMInnerJoin verifies that BlockPartMaxJoinCount = 0 (unlimited) produces
// the full N:M cartesian product within the window.
//
// arrivals: a1, b1, a2, b2  ->  expect {a1·b1, a2·b1, a1·b2, a2·b2} (4 rows).
func TestBlockStore_NMInnerJoin(t *testing.T) {
	keys := state.ColumnKeyDefinitions{{Name: "k"}}

	store := correlate.NewBlockStore(keys, 10)
	ccc := correlate.CacheControlContext{
		BlockPartMaxJoinCount: 0, // 0 => unlimited (N:M)
		BlockWindowTTL:        1 * time.Minute,
		BlockPartMaxAge:       1 * time.Minute,
	}

	var pairs []string
	collect := func(data models.Data) error {
		// each joined row carries both the "a" and "b" non-key fields
		pairs = append(pairs, fmt.Sprintf("%v·%v", data["a"], data["b"]))
		return nil
	}

	require.NoError(t, store.AddData(ccc, "A", models.Data{"k": "K", "a": "a1"}, collect))
	require.NoError(t, store.AddData(ccc, "B", models.Data{"k": "K", "b": "b1"}, collect))
	require.NoError(t, store.AddData(ccc, "A", models.Data{"k": "K", "a": "a2"}, collect))
	require.NoError(t, store.AddData(ccc, "B", models.Data{"k": "K", "b": "b2"}, collect))

	sort.Strings(pairs)
	require.Equal(t, []string{"a1·b1", "a1·b2", "a2·b1", "a2·b2"}, pairs,
		"unlimited max-join-count should yield the full N:M cartesian product")
}

// TestBlockStore_OneToOneJoin verifies the default one-to-one behavior is unchanged:
// each part is consumed by a single match.
//
// arrivals: a1, b1, a2, b2  ->  expect {a1·b1, a2·b2} (2 rows).
func TestBlockStore_OneToOneJoin(t *testing.T) {
	keys := state.ColumnKeyDefinitions{{Name: "k"}}

	store := correlate.NewBlockStore(keys, 10)
	ccc := correlate.CacheControlContext{
		BlockPartMaxJoinCount: 1, // one-to-one
		BlockWindowTTL:        1 * time.Minute,
		BlockPartMaxAge:       1 * time.Minute,
	}

	var pairs []string
	collect := func(data models.Data) error {
		pairs = append(pairs, fmt.Sprintf("%v·%v", data["a"], data["b"]))
		return nil
	}

	require.NoError(t, store.AddData(ccc, "A", models.Data{"k": "K", "a": "a1"}, collect))
	require.NoError(t, store.AddData(ccc, "B", models.Data{"k": "K", "b": "b1"}, collect))
	require.NoError(t, store.AddData(ccc, "A", models.Data{"k": "K", "a": "a2"}, collect))
	require.NoError(t, store.AddData(ccc, "B", models.Data{"k": "K", "b": "b2"}, collect))

	sort.Strings(pairs)
	require.Equal(t, []string{"a1·b1", "a2·b2"}, pairs,
		"one-to-one should consume each part with a single match")
}
