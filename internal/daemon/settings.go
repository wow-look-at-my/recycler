// settings.go holds MaxFreeFraction.

package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/wow-look-at-my/recycler/internal/trash"
)

// MaxFreeFraction caps any free-space goal at total/MaxFreeFraction.
const MaxFreeFraction = 2

// Settings are what a person can change about the daemon. The zero value is
// the built-in behaviour.
type Settings struct {
	// MinFree is the free space the daemon keeps on each filesystem holding a bin, as "5GiB" or "15%".
	MinFree string `json:"min_free,omitempty"`

	// Verbose makes the daemon log each sweep, and why each item went.
	Verbose bool `json:"verbose,omitempty"`
}

// Validate checks every field a person can set.
func (s Settings) Validate() error {
	if s.MinFree == "" {
		return nil
	}
	a, err := ParseAmount(s.MinFree)
	if err != nil {
		return err
	}
	if a.Percent > 100/MaxFreeFraction {
		return fmt.Errorf("recycler: min-free %s is over %d%% of the filesystem; the daemon would give back every recycled item to chase it",
			a, 100/MaxFreeFraction)
	}
	return nil
}

// minFree answers the parsed MinFree. Validate has already accepted it.
func (s Settings) minFree() Amount {
	a, _ := ParseAmount(s.MinFree)
	return a
}

// Trigger is the free space under which the daemon sweeps a filesystem of
// total bytes. It is never under [FreeTarget], where a recycle turns into a
// deletion, and never over total/[MaxFreeFraction].
func (s Settings) Trigger(total uint64) uint64 {
	floor := FreeTarget(total)
	if s.MinFree == "" {
		return floor
	}
	want := s.minFree().Of(total)
	if ceiling := total / MaxFreeFraction; want > ceiling {
		want = ceiling
	}
	return max(want, floor)
}

// Recover is the free space a sweep of a filesystem of total bytes frees up
// to once it starts.
func (s Settings) Recover(total uint64) uint64 {
	return recoverFrom(s.Trigger(total), total)
}

// A Target is what the settings mean on one filesystem holding a bin.
type Target struct {
	Dir     string
	Avail   uint64
	Total   uint64
	Trigger uint64 // the daemon sweeps under this much free
	Recover uint64 // and frees up to this much once it does
}

// Targets reads every filesystem holding a bin and answers what s means there.
// A filesystem that cannot be read is left out.
func Targets(s Settings) ([]Target, error) {
	b, err := trash.Backend()
	if err != nil {
		return nil, err
	}
	var targets []Target
	for _, dir := range b.Dirs() {
		avail, total, err := freeAt(dir)
		if err != nil {
			continue
		}
		targets = append(targets, Target{dir, avail, total, s.Trigger(total), s.Recover(total)})
	}
	return targets, nil
}

// SettingsPath answers the file the settings live in, beside the daemon lock.
func SettingsPath() (string, error) { return statePath("settings.json") }

// LoadSettings reads the settings. A missing file is the built-in behaviour.
func LoadSettings() (Settings, error) {
	path, err := SettingsPath()
	if err != nil {
		return Settings{}, err
	}
	return readSettings(path)
}

func readSettings(path string) (Settings, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return Settings{}, fmt.Errorf("recycler: reading %s: %w", path, err)
	}
	if err := s.Validate(); err != nil {
		return Settings{}, fmt.Errorf("recycler: reading %s: %w", path, err)
	}
	return s, nil
}

// SaveSettings validates s and writes it. A running daemon reads it on its
// next tick. An AI agent session may not change it.
func SaveSettings(s Settings) error {
	if err := refuseAgent("change what the daemon keeps free"); err != nil {
		return err
	}
	if err := s.Validate(); err != nil {
		return err
	}
	path, err := SettingsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "\t")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// settingsCache reads the settings file again only when its stamp changes, so
// a tick costs one stat.
type settingsCache struct {
	path  func() (string, error)
	mod   time.Time
	size  int64
	found bool
	have  bool
	s     Settings
}

// load answers the current settings. A file that cannot be read gives the
// built-in settings, and the daemon logs that once for each change of the file.
func (c *settingsCache) load() Settings {
	path, err := c.path()
	if err != nil {
		return c.s
	}
	st, err := os.Stat(path)
	found := err == nil
	if c.have && found == c.found && (!found || (st.ModTime().Equal(c.mod) && st.Size() == c.size)) {
		return c.s
	}
	c.have, c.found = true, found
	if found {
		c.mod, c.size = st.ModTime(), st.Size()
	}
	s, err := readSettings(path)
	if err != nil {
		notef("%v; the daemon uses its built-in settings until the file is fixed", err)
		s = Settings{}
	} else {
		notef("settings: %s", s.describe())
	}
	c.s = s
	active.Store(&s)
	return s
}

func (s Settings) describe() string {
	free := "a tenth of each filesystem, at most 1 GiB"
	if s.MinFree != "" {
		free = s.MinFree + fmt.Sprintf(" of each filesystem, at least the built-in floor and at most 1/%d of it", MaxFreeFraction)
	}
	return fmt.Sprintf("keep %s free; verbose %t", free, s.Verbose)
}

// active is the settings the daemon last loaded, which decide whether a trace line prints.
var active atomic.Pointer[Settings]

// Verbose turns on the trace lines whatever the settings say.
var Verbose bool

// Logf takes every line the daemon writes about its own work. Nil discards them.
var Logf func(format string, args ...any)

// notef writes a line the daemon always reports.
func notef(format string, args ...any) {
	if Logf != nil {
		Logf(format, args...)
	}
}

// tracef writes a line only a verbose daemon reports.
func tracef(format string, args ...any) {
	if s := active.Load(); Verbose || (s != nil && s.Verbose) {
		notef(format, args...)
	}
}
