//go:build linux || freebsd || netbsd || openbsd || dragonfly || solaris

package recycler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// homeTrash returns the trash directory the isolated test environment uses.
func homeTrash(t *testing.T) string {
	t.Helper()
	b, err := platformBackend()
	if err != nil {
		t.Fatalf("platformBackend: %v", err)
	}
	return b.(*fdoTrash).home
}

// TestTrashInfoIsSpecCompliant checks the metadata this package writes against
// the FreeDesktop trash specification, so other trash tools can read it.
func TestTrashInfoIsSpecCompliant(t *testing.T) {
	work := isolateTrash(t)
	path := writeFile(t, filepath.Join(work, "a file with spaces & symbols.txt"), "x")
	if err := Recycle(path); err != nil {
		t.Fatalf("Recycle: %v", err)
	}

	infoDir := filepath.Join(homeTrash(t), trashInfoDir)
	entries, err := os.ReadDir(infoDir)
	if err != nil {
		t.Fatalf("reading %s: %v", infoDir, err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 .trashinfo file, got %d", len(entries))
	}
	if !strings.HasSuffix(entries[0].Name(), trashInfoExt) {
		t.Errorf("metadata file %q does not end in %s", entries[0].Name(), trashInfoExt)
	}

	raw, err := os.ReadFile(filepath.Join(infoDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines in the metadata file, got %d: %q", len(lines), raw)
	}
	if lines[0] != "[Trash Info]" {
		t.Errorf("first line = %q, want [Trash Info]", lines[0])
	}
	wantPath := "Path=" + escapePath(path)
	if lines[1] != wantPath {
		t.Errorf("second line = %q, want %q", lines[1], wantPath)
	}
	if !strings.Contains(lines[1], "%20") {
		t.Errorf("the path was not percent-encoded: %q", lines[1])
	}
	date, ok := strings.CutPrefix(lines[2], "DeletionDate=")
	if !ok {
		t.Fatalf("third line = %q, want a DeletionDate", lines[2])
	}
	if _, err := time.ParseInLocation(deletionDateLayout, date, time.Local); err != nil {
		t.Errorf("DeletionDate %q is not in the format the specification requires: %v", date, err)
	}
}

// TestReadsForeignTrashEntries checks that entries written by another trash
// implementation - a file manager, say - are listed and restored correctly.
func TestReadsForeignTrashEntries(t *testing.T) {
	work := isolateTrash(t)
	trash := homeTrash(t)
	if err := ensureTrashDir(trash); err != nil {
		t.Fatal(err)
	}

	original := filepath.Join(work, "from another tool.txt")
	writeFile(t, filepath.Join(trash, trashFilesDir, "from another tool.txt"), "foreign")
	info := "[Trash Info]\nPath=" + escapePath(original) + "\nDeletionDate=2026-07-26T11:24:09\n"
	writeFile(t, filepath.Join(trash, trashInfoDir, "from another tool.txt"+trashInfoExt), info)

	items := mustList(t)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].OriginalPath != original {
		t.Errorf("OriginalPath = %q, want %q", items[0].OriginalPath, original)
	}
	if want := time.Date(2026, 7, 26, 11, 24, 9, 0, time.Local); !items[0].DeletedAt.Equal(want) {
		t.Errorf("DeletedAt = %s, want %s", items[0].DeletedAt, want)
	}

	restored, err := Restore(items[0].ID)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored != original {
		t.Errorf("restored to %q, want %q", restored, original)
	}
	if content, err := os.ReadFile(original); err != nil || string(content) != "foreign" {
		t.Errorf("restored content = %q, %v", content, err)
	}
	// Both halves of the entry have to be gone afterwards.
	if _, err := os.Lstat(filepath.Join(trash, trashInfoDir, "from another tool.txt"+trashInfoExt)); err == nil {
		t.Error("the .trashinfo file was left behind after restoring")
	}
}

func TestUnescapeMountPoint(t *testing.T) {
	cases := map[string]string{
		`/mnt/plain`:            `/mnt/plain`,
		`/mnt/with\040space`:    `/mnt/with space`,
		`/mnt/tab\011here`:      "/mnt/tab\there",
		`/mnt/back\134slash`:    `/mnt/back\slash`,
		`/mnt/not\9an\escape`:   `/mnt/not\9an\escape`,
		`/media/user/My\040USB`: `/media/user/My USB`,
	}
	for in, want := range cases {
		if got := unescapeMountPoint(in); got != want {
			t.Errorf("unescapeMountPoint(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTopDirOfTrash(t *testing.T) {
	cases := map[string]string{
		"/media/usb/.Trash-1000":  "/media/usb",
		"/media/usb/.Trash/1000":  "/media/usb",
		"/home/user/.local/share": "",
	}
	for in, want := range cases {
		if got := topDirOfTrash(in); got != want {
			t.Errorf("topDirOfTrash(%q) = %q, want %q", in, got, want)
		}
	}
}
