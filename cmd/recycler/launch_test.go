package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capture runs a command line through the same routing main uses, and returns everything it printed.
func capture(t *testing.T, terminal bool, args ...string) (string, error) {
	t.Helper()
	t.Cleanup(resetCommands)

	out := &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(out)
	rootCmd.SetIn(strings.NewReader(""))
	err := execute(append([]string{"recycler"}, args...), terminal)
	return out.String(), err
}

// Redirected, a bare invocation has nobody to drive a full screen, so it prints the help a script was asking for.
func TestABareInvocationWithoutATerminalPrintsHelp(t *testing.T) {
	isolateTrash(t)

	out, err := capture(t, false)
	require.NoError(t, err)
	assert.Contains(t, out, "Usage:")
	assert.Contains(t, out, "tui", "the help names the command that opens the browser anyway")
}

// A terminal is not enough on its own: an invocation that named a command runs that command.
func TestACommandRunsEvenOnATerminal(t *testing.T) {
	isolateTrash(t)

	out, err := capture(t, true, "list")
	require.NoError(t, err)
	assert.Contains(t, out, "the recycle bin is empty")
}

// The tui command is how the browser is asked for by name, whatever the terminal is doing.
func TestTUICommandIsRegistered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"tui"})
	require.NoError(t, err)
	assert.Equal(t, "tui", cmd.Name())
	require.NotNil(t, cmd.RunE, "the command runs something")
}

// Routing on a bare invocation must not swallow an argument, or a typo would open the browser instead of failing.
func TestAnUnknownCommandIsStillReported(t *testing.T) {
	isolateTrash(t)

	_, err := capture(t, true, "nonsense")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown command")
}
