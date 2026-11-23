//go:build darwin

package daemon

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The daemon runs in the darwin background band, which keeps it on the
// efficiency cores. A nice value alone leaves it free to take a performance core.
func TestTheDaemonRunsInTheBackgroundBand(t *testing.T) {
	t.Cleanup(func() { require.NoError(t, syscall.Setpriority(prioDarwinProcess, 0, 0)) })

	require.True(t, schedule(), "the background band was refused")

	band, err := syscall.Getpriority(prioDarwinProcess, 0)
	require.NoError(t, err)
	assert.NotZero(t, band, "the process is not in the background band")
}
