package dsstore

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A pivot with no node to its right is what panicked a recycle on macOS. The
// encoder must refuse such a tree with an error, not read past the level below.
func TestAPivotWithoutARightChildIsAnErrorNotAPanic(t *testing.T) {
	leaf := []Record{Ustr("a", "ptbL", "x")}
	levels := [][][]Record{{leaf}, {{Ustr("b", "ptbL", "y")}}}
	_, err := dsEncodeNodes(levels)
	require.ErrorIs(t, err, errBadDSStore)
}

// Random name and value lengths reach node boundaries that a fixed record
// shape never reaches, at every level of the tree.
func TestEveryLevelHasAChildOnEachSideOfEveryPivot(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 600; iter++ {
		n := rng.Intn(3000) + 1
		records := make([]Record, 0, n)
		for i := range n {
			name := fmt.Sprintf("%s-%05d", strings.Repeat("x", rng.Intn(60)), i)
			records = append(records, Ustr(name, "ptbL", strings.Repeat("p", rng.Intn(200))))
		}
		sortDSRecords(records)
		levels, err := dsBuildLevels(records)
		require.NoError(t, err, "case %d, %d records", iter, n)
		for level := 1; level < len(levels); level++ {
			pivots := 0
			for _, node := range levels[level] {
				pivots += len(node)
			}
			require.Equal(t, pivots+len(levels[level]), len(levels[level-1]),
				"case %d, %d records: level %d needs a child on each side of every pivot", iter, n, level)
		}
	}
}
