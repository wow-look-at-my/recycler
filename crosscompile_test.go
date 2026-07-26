package recycler

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// crossCompileTargets is every platform this package claims to support. The
// per-platform code cannot be exercised from a single machine, so at the very
// least CI must prove that it all still compiles.
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
		t.Run(strings.ReplaceAll(target, "/", "_"), func(t *testing.T) {
			t.Parallel()
			require.True(t, supported[target], "%s is not a target this Go toolchain knows about; update crossCompileTargets", target)

			cmd := exec.Command(goBin, "build", "-o", filepath.Join(outDir, goos+"_"+goarch), "./...")
			cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, "building for %s failed:\n%s", target, out)
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
	require.NoError(t, err, "cannot find the go command to cross compile with")
	return path
}

// distList returns the set of platforms the toolchain can build for.
func distList(t *testing.T, goBin string) map[string]bool {
	t.Helper()
	out, err := exec.Command(goBin, "tool", "dist", "list").Output()
	require.NoError(t, err, "go tool dist list")

	supported := map[string]bool{}
	for _, line := range strings.Fields(string(out)) {
		supported[line] = true
	}
	return supported
}
