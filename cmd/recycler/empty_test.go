package main

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	agent "github.com/wow-look-at-my/is-this-an-agent"
	"github.com/wow-look-at-my/recycler"
	"github.com/wow-look-at-my/recycler/internal/daemon"
)

// noAgent runs the test as a person: it removes every agent variable and the
// detection of an agent process. It runs the same inside an agent session.
func noAgent(t *testing.T) {
	t.Helper()
	detect := daemon.DetectAgent
	daemon.DetectAgent = func() (agent.Agent, bool) { return agent.Agent{}, false }
	t.Cleanup(func() { daemon.DetectAgent = detect })
	for _, name := range daemon.AgentVars() {
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

// recycle puts a new file named name in the bin.
func recycle(t *testing.T, work, name string) {
	t.Helper()
	_, err := run(t, "", "trash", writeFile(t, filepath.Join(work, name), name))
	require.NoError(t, err)
}

// recycleAged recycles a file and backdates its FreeDesktop record by age.
// The macOS Trash takes the time from the inode change time, which a test cannot move back.
func recycleAged(t *testing.T, work, name string, age time.Duration) {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("the macOS Trash records no deletion time a test can backdate")
	}
	recycle(t, work, name)
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
	recycle(t, work, "kept.txt")

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
	recycle(t, work, "old.txt")

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

func TestConfigAddsAndRemovesFolders(t *testing.T) {
	noAgent(t)
	isolateTrash(t)
	isolateState(t)
	dir := t.TempDir()

	out, err := run(t, "", "config")
	require.NoError(t, err)
	assert.Contains(t, out, "folders: none", "no folder is capped until one is added")

	_, err = run(t, "", "config", "folder", "add", dir, "10GB", "--delete")
	require.NoError(t, err)
	_, err = run(t, "", "config", "folder", "add", "/var/tmp", "2GiB")
	require.NoError(t, err)
	s, err := recycler.LoadDaemonSettings()
	require.NoError(t, err)
	assert.ElementsMatch(t, []daemon.Folder{{Dir: dir, Max: "10GB", Delete: true}, {Dir: "/var/tmp", Max: "2GiB"}}, s.Folders)

	out, err = run(t, "", "config")
	require.NoError(t, err)
	assert.Contains(t, out, "folder: "+dir+" under 10GB, deleting permanently")
	assert.Contains(t, out, "folder: /var/tmp under 2GiB, recycling")

	for _, args := range [][]string{
		{"config", "folder", "add", dir, "100MB"},
		{"config", "folder", "add", "/", "10GB"},
		{"config", "folder", "remove", "/nowhere"},
	} {
		_, err := run(t, "", args...)
		assert.Error(t, err, "%v", args)
	}

	_, err = run(t, "", "config", "folder", "remove", dir)
	require.NoError(t, err)
	s, err = recycler.LoadDaemonSettings()
	require.NoError(t, err)
	assert.Equal(t, []daemon.Folder{{Dir: "/var/tmp", Max: "2GiB"}}, s.Folders)
}

func TestAnAgentSessionCannotCapAFolder(t *testing.T) {
	noAgent(t)
	isolateTrash(t)
	isolateState(t)
	t.Setenv("GROK_BUILD", "1")

	_, err := run(t, "", "config", "folder", "add", t.TempDir(), "10GB", "--delete")
	require.ErrorIs(t, err, recycler.ErrAgentSession)
	_, err = run(t, "", "daemon", "--once", "--folder", t.TempDir()+":10GB:delete")
	require.ErrorIs(t, err, recycler.ErrAgentSession)
}

// A folder named on the daemon command line is swept, and each file it gives back is named.
func TestTheDaemonSweepsAFolderItWasGiven(t *testing.T) {
	noAgent(t)
	isolateTrash(t)
	isolateState(t)
	dir := t.TempDir()
	keep := writeFile(t, filepath.Join(dir, "fresh.txt"), "fresh")

	out, err := run(t, "", "daemon", "--once", "--verbose", "--folder", dir+":1GiB:delete")
	require.NoError(t, err)
	assert.Contains(t, out, "keep "+dir+" under 1GiB, deleting permanently")
	assert.FileExists(t, keep, "a folder under its cap loses nothing")

	_, err = run(t, "", "daemon", "--once", "--folder", dir+":1MB")
	require.Error(t, err, "a cap under the floor is refused")
}

func TestAFolderEvictionSaysWhenTheFileWasLastUsed(t *testing.T) {
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	line := describeEviction(recycler.Eviction{
		Item:     recycler.Item{Name: "x", OriginalPath: "/tmp/x", Size: 1024, DeletedAt: when},
		Reason:   "least recently used in /tmp",
		LastUsed: true,
	})
	assert.Contains(t, line, "/tmp/x (1.0 KiB, last used ")
	assert.Contains(t, line, "): least recently used in /tmp")
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

func TestAnErrorIsPrefixedOnce(t *testing.T) {
	assert.Equal(t, "recycler: boom", errorLine(errors.New("boom")))
	assert.Equal(t, "recycler: item not found", errorLine(errors.New("recycler: item not found")))
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
