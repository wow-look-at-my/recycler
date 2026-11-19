package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/recycler"
	"github.com/wow-look-at-my/recycler/internal/daemon"
)

// noAgent removes every agent variable for the test, so it runs the same
// inside an agent session and outside one.
func noAgent(t *testing.T) {
	t.Helper()
	for _, name := range daemon.AgentVars {
		if old, ok := os.LookupEnv(name); ok {
			require.NoError(t, os.Unsetenv(name))
			t.Cleanup(func() { os.Setenv(name, old) })
		}
	}
}

// isolateState moves the daemon settings into a temporary directory.
func isolateState(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := daemon.StateDir
	daemon.StateDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { daemon.StateDir = old })
	return dir
}

// recycleAged recycles a file and backdates its record by age.
func recycleAged(t *testing.T, work, name string, age time.Duration) {
	t.Helper()
	_, err := run(t, "", "trash", writeFile(t, filepath.Join(work, name), name))
	require.NoError(t, err)
	info := filepath.Join(os.Getenv("XDG_DATA_HOME"), "Trash", "info", name+".trashinfo")
	data, err := os.ReadFile(info)
	require.NoError(t, err)
	when := time.Now().Add(-age).Format("2006-01-02T15:04:05")
	data = regexp.MustCompile(`DeletionDate=.*`).ReplaceAll(data, []byte("DeletionDate="+when))
	require.NoError(t, os.WriteFile(info, data, 0o600))
}

func listedNames(t *testing.T) []string {
	t.Helper()
	items, err := recycler.List()
	require.NoError(t, err)
	var names []string
	for _, it := range items {
		names = append(names, it.Name)
	}
	return names
}

func TestEmptyDeletesWhatIsOlderAndKeepsTheRest(t *testing.T) {
	noAgent(t)
	work := isolateTrash(t)
	recycleAged(t, work, "ancient.txt", 60*24*time.Hour)
	recycleAged(t, work, "recent.txt", 2*24*time.Hour)

	out, err := run(t, "", "empty", "--older-than", "30d", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "would delete "+filepath.Join(work, "ancient.txt"))
	assert.Contains(t, out, "recycled more than 30 days ago")
	assert.NotContains(t, out, "recent.txt")
	assert.ElementsMatch(t, []string{"ancient.txt", "recent.txt"}, listedNames(t), "a dry run deleted something")

	_, err = run(t, "", "empty", "--older-than", "30d")
	require.Error(t, err, "no answer on standard input is no")
	assert.Len(t, listedNames(t), 2)

	_, err = run(t, "y\n", "empty", "--older-than", "30d")
	require.NoError(t, err)
	assert.Equal(t, []string{"recent.txt"}, listedNames(t))

	out, err = run(t, "", "empty", "--older-than", "1d", "--yes")
	require.NoError(t, err)
	assert.Contains(t, out, "deleted "+filepath.Join(work, "recent.txt"))
	assert.Empty(t, listedNames(t))
}

// No value of any selector can empty what was recycled in the last day.
func TestEmptyRefusesValuesThatWouldTakeEverything(t *testing.T) {
	noAgent(t)
	work := isolateTrash(t)
	recycleAged(t, work, "kept.txt", time.Hour)

	for _, args := range [][]string{
		{"empty", "--yes"},
		{"empty", "--yes", "--older-than", "0s"},
		{"empty", "--yes", "--older-than", "1h"},
		{"empty", "--yes", "--max-size", "0B"},
		{"empty", "--yes", "--max-size", "100MB"},
		{"empty", "--yes", "--max-size", "10%"},
		{"empty", "--yes", "--min-free", "99%"},
		{"empty", "--yes", "--min-free", "1000"},
	} {
		_, err := run(t, "", args...)
		assert.Error(t, err, "%v", args)
	}

	out, err := run(t, "", "empty", "--yes", "--max-size", "1GiB", "--min-free", "1GB")
	require.NoError(t, err)
	assert.NotContains(t, out, "deleted")
	assert.Equal(t, []string{"kept.txt"}, listedNames(t))
}

func TestAnAgentSessionCannotEmptyOrConfigure(t *testing.T) {
	noAgent(t)
	work := isolateTrash(t)
	isolateState(t)
	recycleAged(t, work, "old.txt", 60*24*time.Hour)

	for _, name := range []string{"CLAUDECODE", "GROK_BUILD", "CODEX_SANDBOX"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "1")
			_, err := run(t, "", "empty", "--older-than", "30d", "--yes")
			require.ErrorIs(t, err, recycler.ErrAgentSession)
			assert.Contains(t, err.Error(), name)

			_, err = run(t, "", "config", "set", "min-free", "40%")
			require.ErrorIs(t, err, recycler.ErrAgentSession)

			_, err = run(t, "", "config")
			require.NoError(t, err, "reading the settings is fine")
		})
	}
	assert.Equal(t, []string{"old.txt"}, listedNames(t))
}

func TestConfigSetsWhatTheDaemonKeepsFree(t *testing.T) {
	noAgent(t)
	isolateTrash(t)
	isolateState(t)

	out, err := run(t, "", "config")
	require.NoError(t, err)
	assert.Contains(t, out, "min-free: built-in")

	_, err = run(t, "", "config", "set", "min-free", "15%")
	require.NoError(t, err)
	_, err = run(t, "", "config", "set", "verbose", "true")
	require.NoError(t, err)
	s, err := recycler.LoadDaemonSettings()
	require.NoError(t, err)
	assert.Equal(t, recycler.DaemonSettings{MinFree: "15%", Verbose: true}, s)

	out, err = run(t, "", "config")
	require.NoError(t, err)
	assert.Contains(t, out, "min-free: 15%")
	assert.Contains(t, out, "verbose: true")

	for _, args := range [][]string{
		{"config", "set", "min-free", "75%"},
		{"config", "set", "min-free", "20"},
		{"config", "set", "verbose", "maybe"},
		{"config", "set", "colour", "blue"},
	} {
		_, err := run(t, "", args...)
		assert.Error(t, err, "%v", args)
	}

	_, err = run(t, "", "config", "unset", "min-free")
	require.NoError(t, err)
	_, err = run(t, "", "config", "unset", "verbose")
	require.NoError(t, err)
	s, err = recycler.LoadDaemonSettings()
	require.NoError(t, err)
	assert.Equal(t, recycler.DaemonSettings{}, s)
}

func TestTheDaemonLogIsPrinted(t *testing.T) {
	dir := isolateState(t)
	out, err := run(t, "", "daemon", "log")
	require.NoError(t, err)
	assert.Contains(t, out, "no detached daemon has written a log yet")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "daemon.log"), []byte("evicted /x (1 B): disk pressure\n"), 0o600))
	out, err = run(t, "", "daemon", "log")
	require.NoError(t, err)
	assert.Contains(t, out, "evicted /x (1 B): disk pressure")
}

// --verbose traces a sweep that finds nothing to do, which a quiet daemon keeps to itself.
func TestAVerboseOnceSweepSaysWhatItLoaded(t *testing.T) {
	isolateTrash(t)
	isolateState(t)
	out, err := run(t, "", "daemon", "--once", "--verbose")
	require.NoError(t, err)
	assert.Contains(t, out, "settings: keep a tenth of each filesystem")

	out, err = run(t, "", "daemon", "--once")
	require.NoError(t, err)
	assert.NotContains(t, out, "settings:")
}

func TestParseAge(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"30d":  30 * 24 * time.Hour,
		"2w":   14 * 24 * time.Hour,
		"36h":  36 * time.Hour,
		"1.5d": 36 * time.Hour,
	} {
		got, err := parseAge(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, in := range []string{"", "d", "-3d", "soon", "0h"} {
		_, err := parseAge(in)
		assert.Error(t, err, in)
	}
}
