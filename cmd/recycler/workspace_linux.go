//go:build linux && !cosmo

package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/recycler/internal/workspace"
)

func init() {
	var backing string
	var debug bool

	cmd := &cobra.Command{
		Use:   "workspace <mountpoint>",
		Short: "Serve a directory that never reports a full disk",
		Long: `Serve a directory through FUSE, giving back recycled items whenever it fills up.

Recycling defers a deletion, so the space it takes comes back only when something gives the item
up. The daemon does that on a timer, and a program that writes faster than the timer reacts still
meets "no space left on device" and fails.

A write through this mount does not. The mount reclaims recycled items and runs the write again,
so the program sees the second result. It reports a full disk only when the recycle bin has
nothing left to give.

Files live in the backing directory and are served at the mountpoint. Run it in the foreground:
it serves until interrupted, then unmounts.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mountpoint := args[0]
			if backing == "" {
				return fmt.Errorf("recycler: --backing names the directory that holds the files")
			}
			server, err := workspace.Mount(mountpoint, backing, workspace.Options{
				Debug: debug,
				Report: func(freed uint64, err error) {
					if err != nil {
						fmt.Fprintf(os.Stderr, "recycler: reclaiming space: %v\n", err)
						return
					}
					fmt.Fprintf(os.Stderr, "recycler: gave back %d bytes to finish a write\n", freed)
				},
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "recycler: serving %s at %s\n", backing, mountpoint)

			stop := make(chan os.Signal, 1)
			signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
			<-stop
			return server.Unmount()
		},
	}
	cmd.Flags().StringVar(&backing, "backing", "", "the directory holding the files that are served")
	cmd.Flags().BoolVar(&debug, "debug", false, "log the FUSE protocol")
	rootCmd.AddCommand(cmd)
}
