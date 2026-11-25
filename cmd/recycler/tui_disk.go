// tui_disk.go holds the filter on the disk an item takes space on.

package main

import (
	"fmt"
	"path/filepath"
	"slices"

	"github.com/wow-look-at-my/recycler/internal/diskfree"
)

// readDisks learns which disk each item takes space on. A recycled item sits in a bin on its own filesystem, so the
// disk of the bin entry is the disk a delete frees. The lookup runs once for each bin directory, not for each item.
func (m *model) readDisks() {
	m.diskOf = make(map[string]string, len(m.items))
	byDir := map[string]string{}
	m.disks = m.disks[:0]
	for _, item := range m.items {
		dir := filepath.Dir(item.ID)
		disk, ok := byDir[dir]
		if !ok {
			disk = diskOf(dir)
			byDir[dir] = disk
			if !slices.Contains(m.disks, disk) {
				m.disks = append(m.disks, disk)
			}
		}
		m.diskOf[item.ID] = disk
	}
	slices.Sort(m.disks)
	if m.disk != "" && !slices.Contains(m.disks, m.disk) {
		m.disk = ""
	}
}

// cycleDisk narrows the listing to the next disk, and back to every disk after the last.
func (m *model) cycleDisk() {
	if len(m.disks) == 0 {
		return
	}
	at := slices.Index(m.disks, m.disk)
	if at+1 >= len(m.disks) {
		m.setDisk("")
		return
	}
	m.setDisk(m.disks[at+1])
}

func (m *model) setDisk(disk string) {
	m.disk = disk
	m.apply()
	if disk == "" {
		m.say("showing every disk", "muted")
		return
	}
	m.say("showing only what sits on "+disk, "muted")
}

// diskLabel is the header's account of the disk filter: the disk and its free space, or how many disks the bin spans.
func (m *model) diskLabel() string {
	if m.disk == "" {
		if len(m.disks) < 2 {
			return ""
		}
		return fmt.Sprintf("%d disks", len(m.disks))
	}
	avail, total, err := diskfree.Free(m.disk)
	if err != nil {
		return "disk " + m.disk
	}
	return fmt.Sprintf("disk %s · %s free of %s", m.disk, humanSize(int64(avail)), humanSize(int64(total)))
}
