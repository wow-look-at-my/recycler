package bin

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSortNewestFirst(t *testing.T) {
	now := time.Now()
	items := []Item{
		{ID: "old", DeletedAt: now.Add(-time.Hour)},
		{ID: "new", DeletedAt: now},
		{ID: "b", DeletedAt: now.Add(-time.Minute)},
		{ID: "a", DeletedAt: now.Add(-time.Minute)},
	}
	Sort(items)
	assert.Equal(t, []string{"new", "a", "b", "old"}, ids(items))
}

func TestItemStringNamesAnUnknownOrigin(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	known := Item{Name: "notes.txt", OriginalPath: "/home/u/notes.txt", DeletedAt: at}
	assert.Equal(t, "/home/u/notes.txt [2026-01-02T03:04:05Z]", known.String())

	unknown := Item{Name: "notes.txt", DeletedAt: at}
	assert.Equal(t, "notes.txt (original location unknown) [2026-01-02T03:04:05Z]", unknown.String())
}

func ids(items []Item) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.ID
	}
	return out
}
