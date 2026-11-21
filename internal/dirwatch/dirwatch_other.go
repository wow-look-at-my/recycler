//go:build !linux

// dirwatch_other.go holds Watcher.

package dirwatch

// A Watcher cannot be made on this system.
type Watcher struct{}

// New answers ErrUnsupported.
func New() (*Watcher, error) { return nil, ErrUnsupported }

// Add answers ErrUnsupported.
func (*Watcher) Add(string) error { return ErrUnsupported }

// Watching is always false.
func (*Watcher) Watching(string) bool { return false }

// Remove does nothing.
func (*Watcher) Remove(string) {}

// Len is always zero.
func (*Watcher) Len() int { return 0 }

// Read answers ErrUnsupported.
func (*Watcher) Read() (Batch, error) { return Batch{}, ErrUnsupported }

// Close does nothing.
func (*Watcher) Close() error { return nil }
