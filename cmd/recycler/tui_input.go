// tui_input.go holds Update.

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wow-look-at-my/recycler"
	"github.com/wow-look-at-my/tml"
	"github.com/wow-look-at-my/tml/input"
)

// Update takes one message from the program. A key or a click it does not claim goes on to the view, and the view's
// events come back here.
func (m *model) Update(p *tml.Program, msg tml.Msg) bool {
	handled := true
	switch msg := msg.(type) {
	case tml.Resize:
		m.width, m.height = msg.Width, msg.Height
	case tml.Key:
		handled = m.hotkey(msg)
		if m.quitting {
			p.Quit()
		}
	case tml.Mouse:
		// The pointer moving changes nothing here, so it costs no layout.
		if !m.pointer(msg) {
			return false
		}
	case tml.Event:
		m.event(msg)
	default:
		return false
	}
	m.settle()
	return handled
}

// pointer closes an open menu on a press outside it. It reports whether it took the press. A right press elsewhere
// goes on, so it opens a menu of its own where it landed.
func (m *model) pointer(msg tml.Mouse) bool {
	if len(m.menu) == 0 || msg.Action != input.Press {
		return false
	}
	if menu, ok := m.view.UI().Target("menu"); ok && inside(menu.Rect, msg.X, msg.Y) {
		return false
	}
	m.closeMenu()
	return msg.Button != input.ButtonRight
}

func inside(r tml.Rect, x, y int) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// event is one thing the view says the user did.
func (m *model) event(e tml.Event) {
	switch e.Kind {
	case tml.Activated:
		m.activated(e)
	case tml.Menu:
		if e.ID == "rows" {
			m.openMenuAt(e)
		}
	case tml.Scrolled:
		if e.ID == "items" || e.ID == "rows" {
			m.offset += e.Delta
		}
	}
}

func (m *model) activated(e tml.Event) {
	switch {
	case e.Action == "confirm":
		m.confirm()
	case e.Action == "cancel":
		m.cancel()
	case e.Action == "restoreTo":
		m.restoreTo()
	case e.ID == "menuList":
		if i, ok := partIndex(e.Part, "item:"); ok {
			m.runMenu(i)
		}
	case e.ID == "rows":
		m.click(e)
	}
}

// partIndex reads the number out of a part name such as "row:3".
func partIndex(part, prefix string) (int, bool) {
	rest, ok := strings.CutPrefix(part, prefix)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil
}

// click is a left click on the table. A heading sorts on its column. A row selects. Ctrl adds it to the marks.
// Otherwise, Ctrl takes it out, shift marks every row from the last click to it, and a second click on the same
// row restores.
func (m *model) click(e tml.Event) {
	if col, ok := partIndex(e.Part, "header:"); ok {
		m.sortOn(sortKey(col))
		return
	}
	row, ok := partIndex(e.Part, "row:")
	if !ok {
		return
	}
	i := m.first() + row
	if i >= len(m.shown) {
		return
	}
	double := i == m.lastRow && time.Since(m.lastClick) < doubleClick
	m.lastRow, m.lastClick = i, time.Now()
	m.selected = i
	switch {
	case e.Mod&input.Ctrl != 0:
		m.toggleMark(i)
		m.anchor = i
	case e.Mod&input.Shift != 0:
		m.markRange(m.anchor, i)
	default:
		m.anchor = i
		if double {
			m.lastRow = -1
			m.askRestore()
		}
	}
}

// openMenuAt opens the context menu where a right click landed on the table.
func (m *model) openMenuAt(e tml.Event) {
	rows, ok := m.view.UI().Target("rows")
	if !ok {
		return
	}
	x, y := rows.Rect.X+e.X, rows.Rect.Y+e.Y
	if col, ok := partIndex(e.Part, "header:"); ok {
		m.openMenu(x, y, m.sortEntries(sortKey(col)))
		return
	}
	row, ok := partIndex(e.Part, "row:")
	if !ok || m.first()+row >= len(m.shown) {
		return
	}
	m.selected = m.first() + row
	m.openMenu(x, y, m.itemEntries())
}

// openMenuOnSelection opens the context menu from the keyboard, beside the selected row.
func (m *model) openMenuOnSelection() {
	if _, ok := m.current(); !ok {
		return
	}
	x, y := 2, 4
	if rows, ok := m.view.UI().Target("rows"); ok {
		x, y = rows.Rect.X+2, rows.Rect.Y+tableHeaderLines+m.selected-m.first()
	}
	m.openMenu(x, y, m.itemEntries())
}

// itemEntries is the context menu of an item: what can be done to the selection.
func (m *model) itemEntries() []menuEntry {
	targets := m.targets()
	what := ""
	if len(targets) > 1 {
		what = " " + describe(targets)
	}
	mark := "Mark"
	if item, ok := m.current(); ok && m.marks[item.ID] {
		mark = "Unmark"
	}
	return []menuEntry{
		{"Restore" + what, m.askRestore},
		{"Restore" + what + " to…", m.openPrompt},
		{mark, func() { m.toggleMark(m.selected) }},
		{"Mark all / unmark all", m.markAll},
		{"Delete" + what + " permanently…", m.askDelete},
	}
}

// sortEntries is the context menu of a heading.
func (m *model) sortEntries(key sortKey) []menuEntry {
	name := strings.ToLower(columnTitles[key])
	return []menuEntry{
		{"Sort by " + name + ", ascending", func() { m.sortTo(key, false) }},
		{"Sort by " + name + ", descending", func() { m.sortTo(key, true) }},
	}
}

// openMenu shows entries at a point, moved as far as it takes to keep the menu on the screen.
func (m *model) openMenu(x, y int, entries []menuEntry) {
	width := 0
	for _, entry := range entries {
		width = max(width, len([]rune(entry.label)))
	}
	// The list's cursor column and the border around it.
	width += 4
	height := len(entries) + 2
	if y+height > m.height {
		y = y - height + 1
	}
	m.menu, m.menuSelected = entries, 0
	m.menuX = clamp(x, 0, m.width-width)
	m.menuY = clamp(y, 0, m.height-height)
}

func (m *model) closeMenu() {
	m.menu = nil
}

func (m *model) runMenu(i int) {
	if i < 0 || i >= len(m.menu) {
		return
	}
	run := m.menu[i].run
	m.closeMenu()
	run()
}

func (m *model) hotkey(msg tml.Key) bool {
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return true
	case "esc":
		return m.escape()
	}
	switch {
	case len(m.menu) > 0:
		return m.menuKey(msg.String())
	case m.prompting:
		return m.promptKey(msg)
	case m.ask != askNone:
		return m.answer(msg.String())
	case m.filtering:
		return m.typing(msg)
	}
	return m.browsing(msg)
}

// escape backs out of whatever is open, one layer at a time, and quits when nothing is.
func (m *model) escape() bool {
	m.focusNext = ""
	switch {
	case len(m.menu) > 0:
		m.closeMenu()
	case m.prompting:
		m.prompting = false
	case m.ask != askNone:
		m.ask = askNone
	case m.filtering || m.filter != "":
		m.filtering, m.filter = false, ""
		m.apply()
	case len(m.marks) > 0:
		clear(m.marks)
	default:
		m.quitting = true
	}
	return true
}

// menuKey drives an open menu. It takes every key, so a key meant for the listing does not act behind the menu.
func (m *model) menuKey(key string) bool {
	switch key {
	case "up", "k", "shift+tab":
		m.menuSelected = (m.menuSelected + len(m.menu) - 1) % len(m.menu)
	case "down", "j", "tab":
		m.menuSelected = (m.menuSelected + 1) % len(m.menu)
	case "enter", "space":
		m.runMenu(m.menuSelected)
	}
	return true
}

// answer takes a key while a confirmation is open. Enter does what the focused button says, so tabbing to Cancel
// and pressing enter cancels.
func (m *model) answer(key string) bool {
	switch key {
	case "enter":
		if id, _ := m.view.UI().Focused(); id == "cancel" || id == "cancelDanger" {
			m.cancel()
		} else {
			m.confirm()
		}
		return true
	case "y":
		m.confirm()
		return true
	case "n":
		m.cancel()
		return true
	}
	return false
}

func (m *model) typing(msg tml.Key) bool {
	switch msg.String() {
	case "enter":
		m.filtering = false
		return true
	case "backspace":
		if m.filter != "" {
			m.filter = m.filter[:len(m.filter)-1]
			m.apply()
		}
		return true
	case "up", "down":
		return m.browsing(msg)
	}
	// Text is empty for a key that types nothing, and for a key held with ctrl or alt.
	if msg.Text != "" {
		m.filter += msg.Text
		m.apply()
		return true
	}
	return false
}

func (m *model) browsing(msg tml.Key) bool {
	switch msg.String() {
	case "q":
		m.quitting = true
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup":
		m.move(-m.page())
	case "pgdown":
		m.move(m.page())
	case "home", "g":
		m.move(-len(m.shown))
	case "end", "G":
		m.move(len(m.shown))
	case "enter":
		m.askRestore()
	case "space":
		m.toggleMark(m.selected)
		m.move(1)
	case "a":
		m.markAll()
	case "t":
		m.openPrompt()
	case "d", "delete":
		m.askDelete()
	case "s":
		m.sortTo((m.sortBy+1)%sortKey(len(columnTitles)), m.descending)
	case "S":
		m.sortTo(m.sortBy, !m.descending)
	case "m":
		m.openMenuOnSelection()
	case "/":
		m.filtering = true
		m.focusNext = "filter"
	case "r":
		m.reload()
		m.say("reloaded", "muted")
	default:
		return false
	}
	return true
}

// askRestore opens the confirmation for a restore. Restoring writes files back outside the bin, so it is worth a
// keystroke of its own.
func (m *model) askRestore() {
	targets := m.targets()
	if len(targets) == 0 {
		return
	}
	if countUnknown(targets) == len(targets) {
		m.say("original location unknown: restore it with t, or recycler restore --to PATH", "warn")
		return
	}
	m.ask = askRestore
	m.focusNext = "confirm"
}

// askDelete opens the confirmation for a permanent delete. An AI agent session is told no before it is asked.
func (m *model) askDelete() {
	if len(m.targets()) == 0 {
		return
	}
	if why, ok := recycler.AgentSession(); ok {
		m.say("an AI agent session cannot delete from the bin: "+why, "danger")
		return
	}
	m.ask = askDelete
	// The keyboard starts on Cancel, so an enter pressed out of habit keeps the item.
	m.focusNext = "cancelDanger"
}

func (m *model) confirm() {
	ask := m.ask
	m.ask = askNone
	switch ask {
	case askRestore:
		m.restore()
	case askDelete:
		m.delete()
	}
}

func (m *model) cancel() {
	m.ask, m.prompting, m.focusNext = askNone, false, ""
}

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

func (m *model) promptKey(msg tml.Key) bool {
	switch msg.String() {
	case "enter":
		m.restoreTo()
		return true
	case "backspace":
		if m.prompt != "" {
			r := []rune(m.prompt)
			m.prompt = string(r[:len(r)-1])
		}
		return true
	case "tab", "shift+tab":
		return false
	}
	if msg.Text != "" {
		m.prompt += msg.Text
		return true
	}
	return false
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
