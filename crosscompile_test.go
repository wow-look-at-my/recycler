package recycler

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// crossCompileTargets is every platform this package claims to support. The
// per-platform code cannot be exercised from a single machine, so at the very
// least CI must prove it all still compiles.
var crossCompileTargets = []string{
	"linux/amd64",
	"linux/arm64",
	"linux/386",
	"darwin/amd64",
	"darwin/arm64",
	"windows/amd64",
	"windows/386",
	"windows/arm64",
	"freebsd/amd64",
	"openbsd/amd64",
	"netbsd/amd64",
	// A platform with no recycle bin implementation, to keep the fallback
	// compiling.
	"js/wasm",
}

func TestBuildsForEveryPlatform(t *testing.T) {
	if testing.Short() {
		t.Skip("cross compilation is slow")
	}
	goBin := goCommand(t)
	supported := distList(t, goBin)
	outDir := t.TempDir()

	for _, target := range crossCompileTargets {
		goos, goarch, _ := strings.Cut(target, "/")
		if !supported[target] {
			t.Errorf("%s is not a target this Go toolchain knows about; update crossCompileTargets", target)
			continue
		}
		t.Run(strings.ReplaceAll(target, "/", "_"), func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command(goBin, "build", "-o", filepath.Join(outDir, goos+"_"+goarch), "./...")
			cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("building for %s failed: %v\n%s", target, err, out)
			}
		})
	}
}

// goCommand locates the Go toolchain running this test.
func goCommand(t *testing.T) string {
	t.Helper()
	if goroot := runtime.GOROOT(); goroot != "" {
		candidate := filepath.Join(goroot, "bin", "go")
		if runtime.GOOS == "windows" {
			candidate += ".exe"
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	path, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("cannot find the go command to cross compile with: %v", err)
	}
	return path
}

// distList returns the set of platforms the toolchain can build for.
func distList(t *testing.T, goBin string) map[string]bool {
	t.Helper()
	out, err := exec.Command(goBin, "tool", "dist", "list").Output()
	if err != nil {
		t.Fatalf("go tool dist list: %v", err)
	}
	supported := map[string]bool{}
	for _, line := range strings.Fields(string(out)) {
		supported[line] = true
	}
	return supported
}
