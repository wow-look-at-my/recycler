package tml

import (
	"fmt"
	"os"
	"path/filepath"
)

// SocketEnv overrides the path this process serves the inspection protocol on. It is an override and not a switch. A
const SocketEnv = "TML_INSPECT_SOCKET"

// DirEnv overrides the directory the default path is built in. It is what a test sets so its programs do not appear in
const DirEnv = "TML_INSPECT_DIR"

// SocketDir is where a program with no path of its own serves. A single socket per process, named by pid, rather than a shared path
func SocketDir() string {
	if dir := os.Getenv(DirEnv); dir != "" {
		return dir
	}
	if run := os.Getenv("XDG_RUNTIME_DIR"); run != "" {
		return filepath.Join(run, "tml")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("tml-%d", os.Getuid()))
}

// SocketPath is the path this process serves on.
func SocketPath() string {
	if path := os.Getenv(SocketEnv); path != "" {
		return path
	}
	return filepath.Join(SocketDir(), fmt.Sprintf("%d.sock", os.Getpid()))
}

// prepareSocketDir makes the directory readable by this user and nobody else. The socket carries the right to drive
func prepareSocketDir(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cannot create %s: %w", dir, err)
	}
	// MkdirAll leaves an existing directory's mode alone, and the fallback under TMPDIR is a path anybody can have
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("cannot restrict %s to this user: %w", dir, err)
	}
	return nil
}
