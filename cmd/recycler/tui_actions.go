// tui_actions.go holds restore.

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wow-look-at-my/recycler"
)

// restore puts each target back where it came from. One item's failure is reported and the rest still go back.
func (m *model) restore() {
	targets := m.targets()
	restored, failed, skipped := 0, []string{}, 0
	var last string
	for _, item := range targets {
		if item.OriginalPath == "" {
			skipped++
			continue
		}
		path, err := recycler.Restore(item.ID)
		if err != nil {
			failed = append(failed, restoreError(item, err))
			continue
		}
		delete(m.marks, item.ID)
		restored++
		last = path
	}
	m.report("restored", restored, last, failed, skipped)
	m.reload()
}

// restoreError says why an item did not go back, in the words a person acts on.
func restoreError(item recycler.Item, err error) string {
	switch {
	case errors.Is(err, recycler.ErrExists):
		return "something is already at " + item.OriginalPath
	case errors.Is(err, recycler.ErrUnknownOrigin):
		return item.Name + ": original location unknown"
	}
	return err.Error()
}

// report sets the status line after an action on the targets.
func (m *model) report(verb string, done int, last string, failed []string, skipped int) {
	var parts []string
	switch {
	case done == 1 && last != "":
		parts = append(parts, verb+" "+last)
	case done > 0:
		parts = append(parts, fmt.Sprintf("%s %d items", verb, done))
	}
	if skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d with no recorded location stayed in the bin", skipped))
	}
	parts = append(parts, failed...)
	style := "ok"
	switch {
	case len(failed) > 0 && done == 0:
		style = "warn"
		if len(failed) > 1 {
			style = "danger"
		}
	case len(failed) > 0 || skipped > 0:
		style = "warn"
	}
	m.say(strings.Join(parts, "; "), style)
}

// delete removes each target permanently.
func (m *model) delete() {
	targets := m.targets()
	ids := make([]string, len(targets))
	for i, item := range targets {
		ids[i] = item.ID
	}
	evicted, err := recycler.Delete(ids...)
	if err != nil {
		m.say(err.Error(), "danger")
		return
	}
	done, failed := 0, []string{}
	for _, ev := range evicted {
		if ev.Error != nil {
			failed = append(failed, ev.Error.Error())
			continue
		}
		delete(m.marks, ev.Item.ID)
		done++
	}
	last := ""
	if done == 1 && len(targets) == 1 {
		last = targets[0].Name + " permanently"
	}
	m.report("deleted", done, last, failed, 0)
	if done > 1 {
		m.status = strings.Replace(m.status, "items", "items permanently", 1)
	}
	m.reload()
}

// openPrompt asks where to restore the targets, starting from the directory the interface was opened in.
func (m *model) openPrompt() {
	if len(m.targets()) == 0 {
		return
	}
	m.prompting = true
	if m.prompt == "" {
		if wd, err := os.Getwd(); err == nil {
			m.prompt = wd
		}
	}
	m.focusNext = "dest"
}

// restoreTo restores each target into the directory the prompt names, under its own name.
func (m *model) restoreTo() {
	dir := expandHome(strings.TrimSpace(m.prompt))
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		m.say(dir+" is not a directory", "warn")
		return
	}
	m.prompting = false
	restored, failed := 0, []string{}
	var last string
	for _, item := range m.targets() {
		path, err := recycler.RestoreTo(item.ID, filepath.Join(dir, item.Name))
		if err != nil {
			if errors.Is(err, recycler.ErrExists) {
				failed = append(failed, "something is already at "+filepath.Join(dir, item.Name))
			} else {
				failed = append(failed, err.Error())
			}
			continue
		}
		delete(m.marks, item.ID)
		restored++
		last = path
	}
	m.report("restored", restored, last, failed, 0)
	m.reload()
}

// expandHome reads a leading ~ as the home directory, the way a shell would.
func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}
