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
there is room to defer it into. This reads free space every 30 seconds and,
when a filesystem holding a recycle bin has less than a tenth of itself free
(or less than 1 GiB, whichever is smaller), destroys recycled items oldest
first until it does.

Sizes are the ones recorded when each item was recycled, so this never walks
the bin to measure it. An item whose size was never recorded is left alone.

The tool starts this by itself the first time it recycles something. Run it by
hand to watch what it does; a second one exits rather than sweeping alongside
the first.`,
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
	rootCmd.AddCommand(daemonCmd)
}
