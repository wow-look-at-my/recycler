package main

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/recycler"
)

// largeBin opens the interface on a bin of n made-up items, with no recycle bin behind it.
func largeBin(t *testing.T, n int) *model {
	t.Helper()
	isolateTrash(t)
	m, err := newModel()
	require.NoError(t, err)
	now := time.Now()
	for i := range n {
		name := fmt.Sprintf("file-%06d.txt", i)
		m.items = append(m.items, recycler.Item{
			ID:           name,
			Name:         name,
			OriginalPath: filepath.Join("/home/someone/projects/a-fairly-long-directory-name", fmt.Sprint(i%97), name),
			DeletedAt:    now.Add(-time.Duration(i) * time.Minute),
			Size:         int64(i) * 1024,
		})
	}
	m.apply()
	return m
}

// A resize redraws the whole screen. The cost of a frame must follow the rows on screen, not the rows in the bin.
func TestAFrameCostsTheScreenNotTheBin(t *testing.T) {
	m := largeBin(t, 20000)
	start := time.Now()
	for width := 80; width < 120; width++ {
		m.frameOf(width, 40)
	}
	elapsed := time.Since(start)
	assert.Less(t, elapsed, time.Second, "40 frames of a 20000-item bin took %s", elapsed)
}
