package daemon

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/recycler/internal/bin"
)

func TestDeleteTakesOnlyWhatWasPicked(t *testing.T) {
	b := &listing{items: []bin.Item{item("a", 10, 5), item("b", 10, 6)}}
	evicted, err := deleteFrom(b, []string{"/trash/files/b", "/trash/files/nowhere"})
	require.NoError(t, err)
	require.Len(t, evicted, 2)

	assert.Equal(t, "b", evicted[0].Item.Name)
	require.NoError(t, evicted[0].Error)
	assert.ErrorIs(t, evicted[1].Error, bin.ErrNotFound, "an ID the bin does not hold is reported, not evicted")
	assert.Equal(t, []string{"/trash/files/b"}, b.evicted)
}

func TestAnAgentCannotDelete(t *testing.T) {
	t.Serial("replaces DetectAgent and sets an agent variable")
	noAgent(t)
	t.Setenv("CLAUDECODE", "1")
	_, err := Delete([]string{"/trash/files/a"})
	require.ErrorIs(t, err, bin.ErrAgentSession)
}
