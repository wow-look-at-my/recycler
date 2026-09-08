//go:build linux

package daemon

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// threadNice reads the nice value of one thread out of /proc.
func threadNice(t *testing.T, tid string) int {
	t.Helper()
	raw, err := os.ReadFile("/proc/self/task/" + tid + "/stat")
	require.NoError(t, err)
	// The command name is parenthesized and may hold spaces, so the fields after it are what count.
	stat := string(raw)
	fields := strings.Fields(stat[strings.LastIndexByte(stat, ')'):])
	nice, err := strconv.Atoi(fields[17])
	require.NoError(t, err)
	return nice
}

// Naming the caller alone left the runtime's other threads at the priority the daemon started with,
// which is the whole of a sweep's work.
func TestRaisingPriorityCoversEveryThread(t *testing.T) {
	if !raisePriority() {
		t.Skip("no CAP_SYS_NICE here, so nothing could be granted")
	}

	tasks, err := os.ReadDir("/proc/self/task")
	require.NoError(t, err)
	require.Greater(t, len(tasks), 1, "a single-threaded process cannot show the defect")

	for _, task := range tasks {
		assert.Equal(t, daemonNice, threadNice(t, task.Name()),
			"thread %s was left behind", task.Name())
	}
}
