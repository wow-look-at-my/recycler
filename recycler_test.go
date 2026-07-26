package recycler

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// isolateTrash points the recycle bin at a temporary directory so tests never
// touch the developer's real one, and returns a scratch directory to recycle
// files from. Both live on the same filesystem, which is what makes recycling a
// rename.
func isolateTrash(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the Windows recycle bin is a system location and cannot be redirected for tests")
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	work := filepath.Join(root, "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	return work
}

func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustList(t *testing.T) []Item {
	t.Helper()
	items, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return items
}

func TestRecycleAndRestore(t *testing.T) {
	work := isolateTrash(t)
	path := writeFile(t, filepath.Join(work, "notes.txt"), "keep me")

	if err := Recycle(path); err != nil {
		t.Fatalf("Recycle: %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the recycled file is still at its original location: %v", err)
	}

	items := mustList(t)
	if len(items) != 1 {
		t.Fatalf("expected 1 item in the recycle bin, got %d: %v", len(items), items)
	}
	item := items[0]
	if item.Name != "notes.txt" {
		t.Errorf("Name = %q, want notes.txt", item.Name)
	}
	if item.OriginalPath != path {
		t.Errorf("OriginalPath = %q, want %q", item.OriginalPath, path)
	}
	if item.Size != int64(len("keep me")) {
		t.Errorf("Size = %d, want %d", item.Size, len("keep me"))
	}
	if item.IsDir {
		t.Error("IsDir = true, want false")
	}
	if item.DeletedAt.IsZero() {
		t.Error("DeletedAt is zero")
	}

	restored, err := Restore(item.ID)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored != path {
		t.Errorf("restored to %q, want %q", restored, path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the restored file: %v", err)
	}
	if string(content) != "keep me" {
		t.Errorf("restored content = %q, want %q", content, "keep me")
	}
	if items := mustList(t); len(items) != 0 {
		t.Errorf("the recycle bin should be empty after restoring, got %v", items)
	}
}

func TestRecycleDirectory(t *testing.T) {
	work := isolateTrash(t)
	dir := filepath.Join(work, "project")
	writeFile(t, filepath.Join(dir, "a.txt"), "aaa")
	writeFile(t, filepath.Join(dir, "sub", "b.txt"), "bbbb")

	if err := Recycle(dir); err != nil {
		t.Fatalf("Recycle: %v", err)
	}
	items := mustList(t)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if !items[0].IsDir {
		t.Error("IsDir = false, want true for a recycled directory")
	}
	if want := int64(len("aaa") + len("bbbb")); items[0].Size != want {
		t.Errorf("Size = %d, want %d (the total size of the contents)", items[0].Size, want)
	}

	if _, err := Restore(items[0].ID); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "sub", "b.txt"))
	if err != nil {
		t.Fatalf("reading a file from the restored directory: %v", err)
	}
	if string(content) != "bbbb" {
		t.Errorf("restored content = %q, want bbbb", content)
	}
}

func TestRecycleKeepsNamesApart(t *testing.T) {
	work := isolateTrash(t)
	first := writeFile(t, filepath.Join(work, "one", "same.txt"), "first")
	second := writeFile(t, filepath.Join(work, "two", "same.txt"), "second")

	if err := Recycle(first, second); err != nil {
		t.Fatalf("Recycle: %v", err)
	}
	items := mustList(t)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d: %v", len(items), items)
	}
	if items[0].ID == items[1].ID {
		t.Fatalf("both items share the ID %q", items[0].ID)
	}

	// Each one has to restore to its own original location, with its own
	// content.
	for _, item := range items {
		if _, err := Restore(item.ID); err != nil {
			t.Fatalf("Restore %s: %v", item.ID, err)
		}
	}
	for path, want := range map[string]string{first: "first", second: "second"} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if string(content) != want {
			t.Errorf("%s = %q, want %q", path, content, want)
		}
	}
}

func TestRestoreToExplicitDestination(t *testing.T) {
	work := isolateTrash(t)
	path := writeFile(t, filepath.Join(work, "move-me.txt"), "hello")
	if err := Recycle(path); err != nil {
		t.Fatalf("Recycle: %v", err)
	}
	items := mustList(t)

	dest := filepath.Join(work, "elsewhere", "renamed.txt")
	restored, err := RestoreTo(items[0].ID, dest)
	if err != nil {
		t.Fatalf("RestoreTo: %v", err)
	}
	if restored != dest {
		t.Errorf("restored to %q, want %q", restored, dest)
	}
	if content, err := os.ReadFile(dest); err != nil || string(content) != "hello" {
		t.Errorf("restored file = %q, %v", content, err)
	}
}

func TestRestoreRefusesToOverwrite(t *testing.T) {
	work := isolateTrash(t)
	path := writeFile(t, filepath.Join(work, "busy.txt"), "recycled")
	if err := Recycle(path); err != nil {
		t.Fatalf("Recycle: %v", err)
	}
	writeFile(t, path, "new file in the old place")

	items := mustList(t)
	if _, err := Restore(items[0].ID); !errors.Is(err, ErrExists) {
		t.Fatalf("Restore over an existing file: got %v, want ErrExists", err)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "new file in the old place" {
		t.Errorf("the existing file was disturbed: %q, %v", content, err)
	}
	if len(mustList(t)) != 1 {
		t.Error("the item should still be in the recycle bin after a refused restore")
	}
}

func TestPurgeAndEmpty(t *testing.T) {
	work := isolateTrash(t)
	first := writeFile(t, filepath.Join(work, "a.txt"), "a")
	second := writeFile(t, filepath.Join(work, "b.txt"), "b")
	third := writeFile(t, filepath.Join(work, "c.txt"), "c")
	if err := Recycle(first, second, third); err != nil {
		t.Fatalf("Recycle: %v", err)
	}

	items := mustList(t)
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if err := Purge(items[0].ID); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if _, err := os.Lstat(items[0].ID); !errors.Is(err, os.ErrNotExist) {
		t.Error("the purged file is still on disk")
	}
	if remaining := mustList(t); len(remaining) != 2 {
		t.Fatalf("expected 2 items after purging one, got %d", len(remaining))
	}

	if err := Empty(); err != nil {
		t.Fatalf("Empty: %v", err)
	}
	if remaining := mustList(t); len(remaining) != 0 {
		t.Fatalf("the recycle bin is not empty: %v", remaining)
	}
}

func TestUnknownIDsAreRejected(t *testing.T) {
	work := isolateTrash(t)
	outsider := writeFile(t, filepath.Join(work, "innocent.txt"), "do not touch")

	// Recycle something so the trash directories exist.
	if err := Recycle(writeFile(t, filepath.Join(work, "decoy.txt"), "decoy")); err != nil {
		t.Fatalf("Recycle: %v", err)
	}

	for _, id := range []string{"", "not-an-id", outsider, filepath.Join(work, "nope", "files", "x")} {
		if _, err := Get(id); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%q) = %v, want ErrNotFound", id, err)
		}
		if _, err := RestoreTo(id, filepath.Join(work, "out")); !errors.Is(err, ErrNotFound) {
			t.Errorf("RestoreTo(%q) = %v, want ErrNotFound", id, err)
		}
		if err := Purge(id); !errors.Is(err, ErrNotFound) {
			t.Errorf("Purge(%q) = %v, want ErrNotFound", id, err)
		}
	}
	// Nothing outside the recycle bin may be touched by a bad ID.
	if content, err := os.ReadFile(outsider); err != nil || string(content) != "do not touch" {
		t.Fatalf("a rejected ID disturbed a file outside the recycle bin: %q, %v", content, err)
	}
	if len(mustList(t)) != 1 {
		t.Error("rejected IDs changed the contents of the recycle bin")
	}
}

func TestRecycleReportsMissingPaths(t *testing.T) {
	work := isolateTrash(t)
	good := writeFile(t, filepath.Join(work, "good.txt"), "good")

	err := Recycle(filepath.Join(work, "absent.txt"), good)
	if err == nil {
		t.Fatal("expected an error for the missing path")
	}
	if !strings.Contains(err.Error(), "absent.txt") {
		t.Errorf("error %q does not mention the failing path", err)
	}
	// The path that does exist still has to be recycled.
	if items := mustList(t); len(items) != 1 || items[0].Name != "good.txt" {
		t.Errorf("the valid path was not recycled: %v", items)
	}
}

func TestRecycleNothing(t *testing.T) {
	isolateTrash(t)
	if err := Recycle(); err != nil {
		t.Errorf("Recycle() with no paths = %v, want nil", err)
	}
	if err := Purge(); err != nil {
		t.Errorf("Purge() with no ids = %v, want nil", err)
	}
}

func TestListOnAnEmptyBin(t *testing.T) {
	isolateTrash(t)
	items, err := List()
	if err != nil {
		t.Fatalf("List on an untouched system: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected no items, got %v", items)
	}
}

func TestAvailable(t *testing.T) {
	if !Available() {
		t.Skipf("no recycle bin implementation for %s", runtime.GOOS)
	}
}
