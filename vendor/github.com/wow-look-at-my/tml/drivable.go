package tml

import (
	"flag"
	"fmt"
	"os"
	"sync"
	"time"
)

// DriveGrace is how long a program may paint before it has to be drivable. A program is readable the moment it loads,
const DriveGrace = 5 * time.Second

// drivable is the guard on the only hole this library cannot close by construction. tea.NewProgram builds a program tml
type drivable struct {
	mu    sync.Mutex
	timer *time.Timer
}

var drives drivable

// painted is called as soon as per frame, by the View that painted it. The check runs on a TIMER armed here, never on a
func (d *drivable) painted(driven bool) {
	if driven || underTest() {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		return
	}
	// A-shot render -- `tml render`, a golden, a screenshot -- has exited long before this fires, which is what keeps
	d.timer = time.AfterFunc(DriveGrace, func() {
		if inspection.isDriven() {
			return
		}
		d.check()
	})
}

// check is the failure itself, with no timer and no exemptions, so a test can reach it without building a program and
func (d *drivable) check() {
	fmt.Fprintf(os.Stderr, "\ntml: this program has been drawing for %s and the inspector cannot drive it.\n"+
		"Build it with tml.NewProgram (or tml.Run) instead of tea.NewProgram:\n"+
		"\n    program, err := tml.NewProgram(model)\n\n"+
		"tea.NewProgram returns a program this library never sees, so op=key, op=click\n"+
		"and op=restyle have nothing to send to. Reading works either way; driving does\n"+
		"not, and a program the debugger only half works against is not one this library\n"+
		"will keep running.\n", DriveGrace)
	panic("tml: no way to drive this program; build it with tml.NewProgram")
}

// reset disarms the guard. Test-only, and it exists because the guard is process-wide for the same reason the
func (d *drivable) reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
}

// underTest reports whether this binary was built by `go test`. It is not an off switch: the flag exists only in a
func underTest() bool { return flag.Lookup("test.v") != nil }
