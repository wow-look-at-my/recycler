package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	agent "github.com/wow-look-at-my/is-this-an-agent"

	"github.com/wow-look-at-my/recycler/internal/bin"
)

// listing is a fake bin that lists fixed items.
type listing struct {
	fakeBackend
	items []bin.Item
}

func (l *listing) List() ([]bin.Item, error) { return l.items, nil }

// noAgent runs the test as a person: it removes every agent variable and the
// detection of an agent process. The test runs the same inside an agent session.
func noAgent(t *testing.T) {
	t.Helper()
	t.Serial("replaces DetectAgent and unsets the agent variables of the process")
	detect := DetectAgent
	DetectAgent = func() (agent.Agent, bool) { return agent.Agent{}, false }
	t.Cleanup(func() { DetectAgent = detect })
	for _, name := range AgentVars() {
		if old, ok := os.LookupEnv(name); ok {
			require.NoError(t, os.Unsetenv(name))
			t.Cleanup(func() { os.Setenv(name, old) })
		}
	}
}

func days(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

func evictedNames(r EmptyReport) []string {
	var names []string
	for _, ev := range r.Evicted {
		names = append(names, ev.Item.Name)
	}
	return names
}

func TestAnEmptyByAgeTakesOnlyWhatIsOlder(t *testing.T) {
	b := &listing{items: []bin.Item{item("old", 10, 24*40), item("mid", 10, 24*10), item("new", 10, 1)}}
	r, err := emptyBin(b, EmptyCriteria{OlderThan: days(30)}, freeSpace(1000, 2000), time.Now(), false)
	require.NoError(t, err)
	assert.Equal(t, []string{"old"}, evictedNames(r))
	assert.Equal(t, []string{"/trash/files/old"}, b.evicted)
	assert.Contains(t, r.Evicted[0].Reason, "more than 30 days ago")
}

func TestADryRunDeletesNothing(t *testing.T) {
	b := &listing{items: []bin.Item{item("old", 10, 24*40)}}
	r, err := emptyBin(b, EmptyCriteria{OlderThan: days(30)}, freeSpace(1000, 2000), time.Now(), true)
	require.NoError(t, err)
	assert.Equal(t, []string{"old"}, evictedNames(r))
	assert.Empty(t, b.evicted)
}

// A free-space goal takes the oldest items until it is met, and no more.
func TestAnEmptyForFreeSpaceStopsAtTheGoal(t *testing.T) {
	b := &listing{items: []bin.Item{item("a", 300, 24*5), item("b", 300, 24*4), item("c", 300, 24*3)}}
	r, err := emptyBin(b, EmptyCriteria{MinFree: Amount{Bytes: 700}}, freeSpace(200, 2000), time.Now(), false)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, evictedNames(r))
	assert.Empty(t, r.Unmet)
}

// An item an age selector already took counts toward the free-space goal.
func TestSelectorsShareWhatTheyTake(t *testing.T) {
	b := &listing{items: []bin.Item{item("a", 300, 24*50), item("b", 300, 24*4), item("c", 300, 24*3)}}
	r, err := emptyBin(b, EmptyCriteria{OlderThan: days(30), MinFree: Amount{Bytes: 700}},
		freeSpace(200, 2000), time.Now(), true)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, evictedNames(r))
}

func TestAnEmptyForBinSizeStopsAtTheSize(t *testing.T) {
	gib := int64(1 << 30)
	b := &listing{items: []bin.Item{item("a", gib, 24*5), item("b", gib, 24*4), item("c", gib, 24*3)}}
	r, err := emptyBin(b, EmptyCriteria{MaxSize: 2 << 30}, freeSpace(1000, 2000), time.Now(), true)
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, evictedNames(r))
}

// Nothing recycled in the last day goes, whatever a selector asks for. A goal
// that then stays unmet is reported, not chased.
func TestNothingRecycledInTheLastDayIsEmptied(t *testing.T) {
	gib := int64(1 << 30)
	b := &listing{items: []bin.Item{item("old", gib, 48), item("fresh", 4*gib, 2)}}
	r, err := emptyBin(b, EmptyCriteria{MaxSize: 1 << 30, MinFree: Amount{Bytes: 1 << 40}},
		freeSpace(0, 4<<40), time.Now(), false)
	require.NoError(t, err)
	assert.Equal(t, []string{"old"}, evictedNames(r))
	assert.Len(t, r.Unmet, 2)
}

// The bounds are what keep a typo from emptying the whole bin.
func TestUnreasonableSelectorsAreRefused(t *testing.T) {
	for name, c := range map[string]EmptyCriteria{
		"no selector":      {},
		"zero age":         {OlderThan: time.Minute},
		"an hour":          {OlderThan: time.Hour},
		"a tiny bin":       {MaxSize: 1},
		"a 500 MiB bin":    {MaxSize: 500 << 20},
		"over half free":   {MinFree: Amount{Percent: 60}},
		"all of it free":   {MinFree: Amount{Percent: 100}},
		"one byte, an age": {OlderThan: time.Second, MaxSize: 10 << 30},
	} {
		assert.Error(t, c.Validate(), name)
	}
	assert.NoError(t, EmptyCriteria{OlderThan: days(1)}.Validate())
	assert.NoError(t, EmptyCriteria{MaxSize: 1 << 30}.Validate())
	assert.NoError(t, EmptyCriteria{MinFree: Amount{Percent: 50}}.Validate())
}

// A byte goal is checked against each filesystem, before anything goes.
func TestAFreeSpaceGoalOverHalfTheFilesystemIsRefused(t *testing.T) {
	b := &listing{items: []bin.Item{item("a", 300, 24*5)}}
	_, err := emptyBin(b, EmptyCriteria{MinFree: Amount{Bytes: 1500}}, freeSpace(0, 2000), time.Now(), false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "over half")
	assert.Empty(t, b.evicted)
}

func TestAnAgentCannotEmptyTheBin(t *testing.T) {
	t.Serial("replaces DetectAgent and sets agent variables")
	noAgent(t)
	for _, name := range []string{"CLAUDECODE", "GROK_BUILD"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "")
			_, err := Empty(EmptyCriteria{OlderThan: days(30)}, true)
			require.ErrorIs(t, err, bin.ErrAgentSession)
			assert.Contains(t, err.Error(), name)

			require.ErrorIs(t, SaveSettings(Settings{MinFree: "5GiB"}), bin.ErrAgentSession)
			folder := Folder{Dir: t.TempDir(), Max: "2GiB", Delete: true}
			require.ErrorIs(t, ValidateFlags(Settings{Folders: []Folder{folder}}), bin.ErrAgentSession)
		})
	}

	// A process under an agent is refused with no variable set at all.
	DetectAgent = func() (agent.Agent, bool) { return agent.Agent{Name: "Claude"}, true }
	_, err := Empty(EmptyCriteria{OlderThan: days(30)}, true)
	require.ErrorIs(t, err, bin.ErrAgentSession)
	assert.Contains(t, err.Error(), "runs under Claude")
}

// The shared roster and the markers named here both count.
func TestAgentVarsCoverTheRosterAndTheNamedMarkers(t *testing.T) {
	vars := AgentVars()
	for _, name := range []string{"CLAUDECODE", "GROK_BUILD", "GROK_AGENT", "CODEX_SANDBOX", "GEMINI_CLI", "OPENCODE"} {
		assert.Contains(t, vars, name)
	}
}

func TestParseAmount(t *testing.T) {
	for in, want := range map[string]Amount{
		"20GB":    {Bytes: 20e9},
		"5GiB":    {Bytes: 5 << 30},
		"1.5 GiB": {Bytes: 3 << 29},
		"500mib":  {Bytes: 500 << 20},
		"15%":     {Percent: 15},
		"2T":      {Bytes: 2 << 40},
	} {
		got, err := ParseAmount(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, in := range []string{"", "20", "GB", "-5GB", "0GB", "5 parsecs", "0%", "150%", "x%"} {
		_, err := ParseAmount(in)
		assert.Error(t, err, in)
	}
	assert.Equal(t, uint64(200), Amount{Percent: 10}.Of(2000))
	assert.Equal(t, "15%", Amount{Percent: 15}.String())
	assert.Equal(t, "5.0 GiB", Amount{Bytes: 5 << 30}.String())
}

// A goal never goes under the built-in floor, where a recycle deletes, and
// never over half the filesystem.
func TestMinFreeIsHeldBetweenTheFloorAndHalf(t *testing.T) {
	tib := uint64(1) << 40
	assert.Equal(t, FreeTarget(tib), Settings{}.Trigger(tib))
	assert.Equal(t, uint64(20<<30), Settings{MinFree: "20GiB"}.Trigger(tib))
	assert.Equal(t, FreeTarget(tib), Settings{MinFree: "1MiB"}.Trigger(tib), "under the floor")
	assert.Equal(t, uint64(1000), Settings{MinFree: "1TB"}.Trigger(2000), "over half")
	assert.Equal(t, tib/5, Settings{MinFree: "20%"}.Trigger(tib))

	assert.Equal(t, RecoverTarget(tib), Settings{}.Recover(tib))
	assert.GreaterOrEqual(t, Settings{MinFree: "40%"}.Recover(tib), Settings{MinFree: "40%"}.Trigger(tib))

	require.Error(t, Settings{MinFree: "60%"}.Validate())
	require.Error(t, Settings{MinFree: "lots"}.Validate())
	require.NoError(t, Settings{MinFree: "50%"}.Validate())
}

// A sweep under a raised goal gives back what the built-in goal keeps.
func TestASweepFollowsMinFree(t *testing.T) {
	tib := uint64(1) << 40
	items := []bin.Item{item("a", 1<<30, 5)}
	b := &fakeBackend{}
	evicted, _, err := sweepItems(b, items, dirs, freeSpace(5<<30, tib), Settings{})
	require.NoError(t, err)
	assert.Empty(t, evicted)

	evicted, _, err = sweepItems(b, items, dirs, freeSpace(5<<30, tib), Settings{MinFree: "10GiB"})
	require.NoError(t, err)
	require.Len(t, evicted, 1)
	assert.Contains(t, evicted[0].Reason, "under its 10.0 GiB trigger")
}

func TestSettingsRoundTrip(t *testing.T) {
	t.Serial("replaces StateDir")
	noAgent(t)
	dir := t.TempDir()
	old := StateDir
	StateDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { StateDir = old })

	s, err := LoadSettings()
	require.NoError(t, err)
	assert.Equal(t, Settings{}, s, "no file is the built-in settings")

	require.NoError(t, SaveSettings(Settings{MinFree: "5GiB", Verbose: true}))
	s, err = LoadSettings()
	require.NoError(t, err)
	assert.Equal(t, Settings{MinFree: "5GiB", Verbose: true}, s)

	require.Error(t, SaveSettings(Settings{MinFree: "90%"}))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{"), 0o600))
	_, err = LoadSettings()
	assert.Error(t, err, "a broken file is an error, not the built-in settings")
}

// A verbose daemon says what it found and what it kept, not only what went.
func TestAVerboseSweepTracesItsWork(t *testing.T) {
	t.Serial("replaces Logf and Verbose")
	var lines []string
	Logf = func(format string, args ...any) { lines = append(lines, format) }
	Verbose = true
	active.Store(nil)
	t.Cleanup(func() { Logf, Verbose = nil, false })

	unknown := item("unmeasured", bin.SizeUnknown, 9)
	_, _, err := sweepItems(&fakeBackend{}, []bin.Item{unknown, item("a", 100, 5)}, dirs, freeSpace(0, 2000), Settings{})
	require.NoError(t, err)
	assert.Contains(t, lines, "%s: %s free of %s, under the %s trigger; giving back the oldest items until %s is free")
	assert.Contains(t, lines, "kept %s: its size is unknown, so giving it back cannot be accounted for")
	assert.Contains(t, lines, "%s: %s free after the sweep")

	lines = nil
	Verbose = false
	_, _, err = sweepItems(&fakeBackend{}, []bin.Item{item("a", 100, 5)}, dirs, freeSpace(0, 2000), Settings{})
	require.NoError(t, err)
	assert.Empty(t, lines, "a quiet daemon traces nothing")
}
