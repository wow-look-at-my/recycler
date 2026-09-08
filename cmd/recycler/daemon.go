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
	"github.com/wow-look-at-my/recycler"
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

Sizes are the ones recorded when each item was recycled, so a sweep does not
walk the bin to measure it. An item whose size is unknown is left alone.

The tool starts this by itself the first time it recycles something.
"recycler daemon up" starts it up front. Run it by hand to watch what it does;
a second one exits rather than sweeping alongside the first.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		interval, _ := cmd.Flags().GetDuration("interval")
		once, _ := cmd.Flags().GetBool("once")
		out := cmd.OutOrStdout()

		if once {
			evicted, err := recycler.Sweep()
			reportEvictions(out, evicted, err)
			return err
		}

		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		err := recycler.RunDaemon(ctx, interval, func(evicted []recycler.Eviction, err error) {
			reportEvictions(out, evicted, err)
		})
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	},
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
		fmt.Fprintf(out, "evicted %s (%d bytes, recycled %s)\n",
			ev.Item.Name, ev.Item.Size, ev.Item.DeletedAt.Format(time.RFC3339))
	}
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
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "the disk-pressure daemon is already running")
		}
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
	daemonCmd.Flags().Duration("interval", recycler.DefaultPollInterval, "how often to read free space")
	daemonCmd.Flags().Bool("once", false, "sweep once and exit")
	daemonCmd.AddCommand(daemonUpCmd)
	rootCmd.AddCommand(daemonCmd)
}
