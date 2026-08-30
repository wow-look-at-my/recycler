//go:build linux || freebsd || netbsd || openbsd || dragonfly || solaris

package recycler

import (
	"bufio"
	"github.com/wow-look-at-my/go-containers/set"
	"os"
	"path/filepath"
	"strings"
)

// pseudoFilesystems never hold user data, so their mount points are not worth
// scanning for trash directories.
var pseudoFilesystems = set.Of(
	"autofs", "bpf", "cgroup", "cgroup2", "configfs",
	"debugfs", "devpts", "devtmpfs", "efivarfs", "fuse.gvfsd-fuse",
	"fusectl", "hugetlbfs", "mqueue", "proc", "pstore",
	"securityfs", "sysfs", "tracefs",
)

// mountPoints returns directories that may be the top directory of a
// filesystem with its own trash directory.
func mountPoints() []string {
	seen := set.New[string]()
	var points []string
	add := func(p string) {
		if p == "" || seen.Contains(p) {
			return
		}
		seen.Add(p)
		points = append(points, p)
	}

	for _, p := range systemMountPoints() {
		add(p)
	}
	// Removable media managed by desktop environments, for systems where the
	// mount table is not readable.
	for _, pattern := range []string{"/media/*", "/media/*/*", "/mnt/*", "/run/media/*/*", "/Volumes/*"} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, m := range matches {
			if fi, err := os.Stat(m); err == nil && fi.IsDir() {
				add(m)
			}
		}
	}
	add("/")
	return points
}

// systemMountPoints reads the kernel's mount table when one is available.
func systemMountPoints() []string {
	f, err := os.Open("/proc/self/mounts")
	if err != nil {
		return nil
	}
	defer f.Close()

	var points []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 || pseudoFilesystems.Contains(fields[2]) {
			continue
		}
		points = append(points, unescapeMountPoint(fields[1]))
	}
	return points
}

// unescapeMountPoint decodes the octal escapes the kernel writes for spaces and
// other special characters in mount point paths.
func unescapeMountPoint(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
			b.WriteByte((s[i+1]-'0')<<6 | (s[i+2]-'0')<<3 | (s[i+3] - '0'))
			i += 3
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }
