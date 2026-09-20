package tml

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultWatchInterval is how often Watch looks for changes. Terminal layout is iterated on by saving and looking, so
const DefaultWatchInterval = 250 * time.Millisecond

// Watch reloads a view whenever any .tml file under dir changes, calling onChange with the result. It runs until ctx
func Watch(ctx context.Context, dir, entry string, opts Options, onChange func(*View, error)) {
	WatchInterval(ctx, dir, entry, opts, DefaultWatchInterval, onChange)
}

// WatchInterval is Watch at a rate the caller picks. How fast a save should show up is the caller's judgement, not
func WatchInterval(ctx context.Context, dir, entry string, opts Options, interval time.Duration, onChange func(*View, error)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	previous, err := fingerprint(dir)
	if err != nil {
		onChange(nil, err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current, err := fingerprint(dir)
			if err != nil {
				onChange(nil, err)
				continue
			}
			if current == previous {
				continue
			}
			previous = current
			onChange(Load(os.DirFS(dir), entry, opts))
		}
	}
}

// fingerprint summarises every .tml file under dir by name, size and modification time. Any add, delete or edit
func fingerprint(dir string) (string, error) {
	var b strings.Builder
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".tml") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		b.WriteString(path)
		b.WriteByte(0)
		b.WriteString(info.ModTime().String())
		b.WriteByte(0)
		// Size is included because a pair of saves inside a single modification-time tick would otherwise look identical.
		b.WriteString(strconv.FormatInt(info.Size(), 10))
		b.WriteByte('\n')
		return nil
	})
	return b.String(), err
}
