// The bin this watches is the account's, not the one its starter exported.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/recycler"
	"github.com/wow-look-at-my/recycler/internal/daemon"
	"github.com/wow-look-at-my/recycler/internal/pressure"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Keep the recycle bin from filling the disk",
	Long: `Watch free space and give back the oldest recycled items when it runs low.

Recycling defers a deletion instead of performing one, which works only while
there is room to defer it into. This reads free space on the interval below
and, when a filesystem holding a recycle bin has less than a tenth of itself
free (or less than 1 GiB, whichever is smaller), destroys recycled items
oldest first until it does.

Free space is what this caller can reach. A filesystem that reserves blocks for
another user reports them as free without ever handing them over, and a target
read off the whole device would ask for space that never arrives.

Every filesystem holding a bin is read on every tick, including one whose bin is
empty. A filesystem left under its target is named, because a bin holds only
what was recycled: a disk filling with files nobody recycled leaves this running
and unable to help, and that is the state worth hearing about.

The bin itself is listed only when that can change something: on the tick a
filesystem runs short, when a short bin changes, and after an eviction. A bin
that changed on a filesystem with room is listed within a minute, which is when
build output past its budget goes. On Linux directory watches keep the listing
current, and a change reads only the entry it names.

Sizes are the ones recorded when each item was recycled, so a sweep does not
walk the bin to measure it. An item whose size is unknown is left alone.

The tool starts this by itself the first time it recycles something.
"recycler daemon up" starts it up front, and "recycler daemon install" has the
service manager start it at login and restart it when it exits. Run it by hand
to watch what it does; a second one exits rather than sweeping alongside the
first, or with --standby waits for the first to stop and then takes over.

A user has one daemon per system. Its lock and its home recycle bin come from
the account database, so a caller that exports another HOME, XDG_CACHE_HOME or
XDG_DATA_HOME reaches the same daemon as every other caller. On Linux the
daemon also stops any other daemon of the same user that holds a different
lock, which is what an older build left behind under such a caller.

Every item the daemon gives back is logged with where it came from, its size
and the rule that chose it. --verbose, or "recycler config set verbose true"
for the daemon the tool starts by itself, also logs each sweep: the free space
it found, the goal it swept to and each item it had to keep. "recycler daemon
log" prints what a detached daemon wrote. "recycler config" sets how much free
space the daemon keeps.

--folder DIR:SIZE also keeps a directory under SIZE, giving back its least
recently used files first: to the recycle bin, or permanently with
DIR:SIZE:delete. "recycler config folder add" does the same for the daemon
the tool starts by itself. No folder is capped unless one is named. An AI
agent session cannot name one.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		interval, _ := cmd.Flags().GetDuration("interval")
		once, _ := cmd.Flags().GetBool("once")
		standby, _ := cmd.Flags().GetBool("standby")
		out := cmd.OutOrStdout()
		daemon.Verbose, _ = cmd.Flags().GetBool("verbose")
		folders, _ := cmd.Flags().GetStringArray("folder")
		var flags daemon.Settings
		for _, spec := range folders {
			f, err := daemon.ParseFolder(spec)
			if err != nil {
				return err
			}
			flags = flags.WithFolder(f)
		}
		if err := daemon.ValidateFlags(flags); err != nil {
			return err
		}
		daemon.Flags = flags
		defer func() { daemon.Flags = daemon.Settings{} }()
		daemon.Logf = func(format string, args ...any) {
			fmt.Fprintf(out, "%s %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
		}
		defer func() { daemon.Logf, daemon.Verbose = nil, false }()

		if once {
			evicted, pressures, err := recycler.Sweep()
			reportSweep(out, evicted, pressures, err)
			return err
		}

		// The bin this watches is the account's, not the one its starter exported.
		if err := daemon.UseAccountEnvironment(); err != nil {
			return err
		}

		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		// Repeating an unchanged complaint on every tick buries the change that matters, in a log nobody can then read.
		seen := map[string]bool{}
		var reaped time.Time
		run := recycler.RunDaemon
		if standby {
			run = recycler.RunDaemonStandby
		}
		err := run(ctx, interval, func(evicted []recycler.Eviction, pressures []recycler.Pressure, err error) {
			reportEvictions(out, evicted, err)
			reportPressureChanges(out, pressures, seen)
			if time.Since(reaped) >= reapInterval {
				reaped = time.Now()
				reapStrays(out)
			}
		})
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	},
}

// reapInterval is how often the daemon holding the lock looks for another.
const reapInterval = 10 * time.Second

// reapStrays stops every other daemon of this user, and names each. Only one
// daemon may sweep per system.
func reapStrays(out io.Writer) {
	lock, err := recycler.DaemonLockPath()
	if err == nil {
		var pids []int
		pids, err = daemon.ReapStrays(lock)
		for _, pid := range pids {
			fmt.Fprintf(out, "stopped daemon %d, which held a lock other than %s\n", pid, lock)
		}
	}
	if err != nil {
		fmt.Fprintf(out, "could not look for a second daemon: %v\n", err)
	}
}

// reportEvictions names what a sweep destroyed. Giving a file back is not a
// thing to do quietly.
func reportEvictions(out io.Writer, evicted []recycler.Eviction, err error) {
	if err != nil {
		fmt.Fprintf(out, "sweep failed: %v\n", err)
	}
	for _, ev := range evicted {
		if ev.Error != nil {
			fmt.Fprintf(out, "could not evict %s: %v\n", ev.Item.Name, ev.Error)
			continue
		}
		fmt.Fprintf(out, "evicted %s\n", describeEviction(ev))
	}
}

// describeEviction names an item by where it came from, what it weighed, when
// it was recycled and what chose it.
func describeEviction(ev recycler.Eviction) string {
	where := ev.Item.OriginalPath
	if where == "" {
		where = ev.Item.Name
	}
	when := "recycled"
	if ev.LastUsed {
		when = "last used"
	}
	line := fmt.Sprintf("%s (%s, %s %s)", where, humanSize(ev.Item.Size), when,
		ev.Item.DeletedAt.Local().Format(time.RFC3339))
	if ev.Reason != "" {
		line += ": " + ev.Reason
	}
	return line
}

// reportSweep is a single sweep's whole account, for a run that ends after it.
func reportSweep(out io.Writer, evicted []recycler.Eviction, pressures []recycler.Pressure, err error) {
	reportEvictions(out, evicted, err)
	for _, p := range pressures {
		fmt.Fprintln(out, pressureLine(p))
	}
}

// reportPressureChanges names a filesystem the sweep could not bring back up to
// its target, and names it again when it recovers. A daemon that stays silent
// here is indistinguishable from a single keeping up, which is the state to
// report loudest. Everything filling the disk is live, and nothing recycled is
// left to give back.
func reportPressureChanges(out io.Writer, pressures []recycler.Pressure, seen map[string]bool) {
	now := set.New[string]()
	for _, p := range pressures {
		now.Add(p.Dir)
		if !seen[p.Dir] {
			fmt.Fprintln(out, pressureLine(p))
		}
	}
	for dir := range seen {
		if !now.Contains(dir) {
			fmt.Fprintf(out, "%s is back above its target\n", dir)
			delete(seen, dir)
		}
	}
	for dir := range now.All() {
		seen[dir] = true
	}
}

func pressureLine(p recycler.Pressure) string {
	line := fmt.Sprintf("cannot keep %s above its target: %s available, %s short",
		p.Dir, pressure.Bytes(p.Avail), pressure.Bytes(p.Shortfall()))
	if p.Stranded > 0 {
		return fmt.Sprintf("%s, and %s of recycled items would not go", line, pressure.Bytes(p.Stranded))
	}
	// Build output is what fills a filesystem nobody can otherwise explain.
	return line + `, and nothing recycled is left to give back. "recycler trim" recycles the build output nobody recycled`
}

var daemonUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Start the daemon in the background if it is not already running",
	Long: `Start a detached daemon and return.

Something has to bring the daemon up before the first thing is recycled: until
then nothing is watching free space, and a machine can fill the disk long
before it deletes anything. This is safe to run from a login script or on a
timer. It takes the daemon's own lock, so a second one finds it held and
returns rather than sweeping alongside the first.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		started, err := recycler.EnsureDaemon(exe)
		if err != nil {
			return err
		}
		if started {
			fmt.Fprintln(cmd.OutOrStdout(), "started the disk-pressure daemon")
			return nil
		}
		if running, ok := recycler.RunningDaemon(); ok {
			fmt.Fprintf(cmd.OutOrStdout(), "the disk-pressure daemon is already running: %s, which this build does not replace\n", running)
			return nil
		}
		fmt.Fprintln(cmd.OutOrStdout(), "the disk-pressure daemon is already running")
		return nil
	},
}

var daemonLogCmd = &cobra.Command{
	Use:   "log",
	Short: "Print what the detached daemon wrote, and where",
	Long: `Print the log of the daemon the tool starts by itself.

Every item that daemon gives back is in here. A daemon a service manager runs
writes to that manager's log instead, such as journalctl --user -u recycler.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := daemon.LogPath()
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: no detached daemon has written a log yet\n", path)
			return nil
		}
		if err != nil {
			return err
		}
		defer f.Close()
		fmt.Fprintf(cmd.ErrOrStderr(), "%s:\n", path)
		_, err = io.Copy(cmd.OutOrStdout(), f)
		return err
	},
}

var daemonInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Have the service manager start the daemon at login and restart it when it exits",
	Long: `Register this executable's daemon with the platform's service manager.

On macOS this writes a LaunchAgent and loads it. On Linux it writes a systemd
user unit, enables it and starts it. On Windows it registers a scheduled task
that runs at logon. Each one starts "recycler daemon --standby" and starts it
again whenever it exits, so a crash or a reboot does not leave the disk
unwatched.

Running it again replaces what an earlier install wrote, which is how a moved
executable is picked up. Nothing from the environment this runs with is written
into the service: the daemon finds its lock and its home recycle bin from the
account database, wherever HOME or the XDG variables point.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		path, err := recycler.InstallDaemon(exe)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "installed the disk-pressure daemon: %s\n", path)
		return nil
	},
}

const noDaemonEnv = "RECYCLER_NO_DAEMON"

// startDaemon brings the daemon up after a recycle.
func startDaemon(stderr io.Writer) {
	if os.Getenv(noDaemonEnv) != "" {
		return
	}
	exe, err := os.Executable()
	if err == nil {
		_, err = recycler.EnsureDaemon(exe)
	}
	if err != nil {
		fmt.Fprintf(stderr, "recycler: could not start the disk-pressure daemon: %v\n", err)
	}
}

func init() {
	// The daemon is ordered against other daemons by the version printed here.
	recycler.SetDaemonVersion(version)
	daemonCmd.Flags().Duration("interval", recycler.DefaultPollInterval, "how often to read free space")
	daemonCmd.Flags().Bool("once", false, "sweep once and exit")
	daemonCmd.Flags().Bool("standby", false, "wait for a running daemon to stop instead of exiting")
	daemonCmd.Flags().BoolP("verbose", "v", false, "log each sweep, and why each item went or stayed")
	daemonCmd.Flags().StringArray("folder", nil,
		"keep DIR:SIZE[:delete|:recycle] under SIZE, least recently used files first (repeatable; recycles unless :delete)")
	daemonCmd.AddCommand(daemonUpCmd)
	daemonCmd.AddCommand(daemonLogCmd)
	daemonCmd.AddCommand(daemonInstallCmd)
	rootCmd.AddCommand(daemonCmd)
}
