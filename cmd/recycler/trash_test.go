package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/wow-look-at-my/recycler"
)

// What left for good is said on standard error, and --quiet cannot reach it.
func TestQuietNeverHidesWhatWasDestroyed(t *testing.T) {
	disposals := []recycler.Disposal{
		{
			Path:   "/work/new.txt",
			Reason: "only 400 B free",
			Evicted: []recycler.Item{
				{Name: "old.bin", OriginalPath: "/work/old.bin", Size: 500, DeletedAt: time.Now().Add(-time.Hour)},
			},
		},
		{Path: "/work/gone.txt", Permanent: true, Reason: "the bin holds too little"},
	}

	for _, quiet := range []bool{false, true} {
		var out, errOut bytes.Buffer
		recycled := reportDisposals(&out, &errOut, disposals, quiet)

		assert.Equal(t, 1, recycled, "a deleted path counts as recycled")
		assert.Contains(t, errOut.String(), "evicted /work/old.bin")
		assert.Contains(t, errOut.String(), "to make room for /work/new.txt: only 400 B free")
		assert.Contains(t, errOut.String(), "deleted /work/gone.txt permanently, not recycled: the bin holds too little")
		assert.NotContains(t, out.String(), "gone.txt", "a deleted path was reported as recycled")
		assert.Equal(t, !quiet, bytes.Contains(out.Bytes(), []byte("recycled /work/new.txt")))
	}
}
