package recycler

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUniqueName(t *testing.T) {
	dir := t.TempDir()
	if got := uniqueName("report.txt", dir); got != "report.txt" {
		t.Errorf("uniqueName in an empty directory = %q, want report.txt", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "report.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := uniqueName("report.txt", dir); got != "report_1.txt" {
		t.Errorf("uniqueName with the name taken = %q, want report_1.txt", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "report_1.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := uniqueName("report.txt", dir); got != "report_2.txt" {
		t.Errorf("uniqueName with two names taken = %q, want report_2.txt", got)
	}
	if got := uniqueName("..", dir); got != "recycled" {
		t.Errorf("uniqueName(\"..\") = %q, want recycled", got)
	}
}

func TestMoveRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	for _, path := range []string{src, dst} {
		if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := move(src, dst); !errors.Is(err, ErrExists) {
		t.Fatalf("move onto an existing file = %v, want ErrExists", err)
	}
	if content, err := os.ReadFile(dst); err != nil || string(content) != dst {
		t.Errorf("the destination was modified: %q, %v", content, err)
	}
}

func TestCopyTree(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "tree")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "file.txt"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file.txt", filepath.Join(src, "sub", "link")); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "copy")
	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree: %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(dst, "sub", "file.txt")); err != nil || string(content) != "content" {
		t.Errorf("copied file = %q, %v", content, err)
	}
	target, err := os.Readlink(filepath.Join(dst, "sub", "link"))
	if err != nil || target != "file.txt" {
		t.Errorf("copied symlink = %q, %v", target, err)
	}
}

func TestTreeSize(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b"), []byte("123"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := treeSize(dir); got != 8 {
		t.Errorf("treeSize = %d, want 8", got)
	}
	if got := treeSize(filepath.Join(dir, "a")); got != 5 {
		t.Errorf("treeSize of a file = %d, want 5", got)
	}
	if got := treeSize(filepath.Join(dir, "missing")); got != SizeUnknown {
		t.Errorf("treeSize of a missing path = %d, want SizeUnknown", got)
	}
}

func TestSortItemsNewestFirst(t *testing.T) {
	now := time.Now()
	items := []Item{
		{ID: "old", DeletedAt: now.Add(-time.Hour)},
		{ID: "new", DeletedAt: now},
		{ID: "b", DeletedAt: now.Add(-time.Minute)},
		{ID: "a", DeletedAt: now.Add(-time.Minute)},
	}
	sortItems(items)
	want := []string{"new", "a", "b", "old"}
	for i, id := range want {
		if items[i].ID != id {
			t.Fatalf("sorted order = %v, want %v", ids(items), want)
		}
	}
}

func ids(items []Item) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.ID
	}
	return out
}
