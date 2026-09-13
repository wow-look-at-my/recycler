package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/recycler"
)

var trashCmd = &cobra.Command{
	Use:     "trash <path>...",
	Aliases: []string{"rm", "delete"},
	Short:   "Move files and directories to the recycle bin",
	Long: `Move each path to the recycle bin, where it can be restored from later.

Directories are recycled whole. Paths that cannot be recycled are reported,
without stopping the remaining ones.

A recycle bin lives on the filesystem it takes from, so recycling moves the
bytes rather than freeing them. When a filesystem is already under the space the
daemon keeps free, or when a path is larger than what is left, moving it there
cannot help and would spend more of what is left on the record of the move. Such
a path is deleted outright instead, and saying so is not optional: the line
naming it goes to standard error and --quiet does not suppress it.`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		quiet, _ := cmd.Flags().GetBool("quiet")
		// A call per path, so a failure affects only its own path.
		var errs []error
		recycled := 0
		for _, path := range args {
			disposals, err := recycler.Recycle(path)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			for _, d := range disposals {
				if d.Permanent {
					// Never quiet. A caller who believes this went to the bin
					// will go looking for it, and there is nothing to find.
					fmt.Fprintf(cmd.ErrOrStderr(), "deleted %s permanently, not recycled: %s\n",
						path, d.Reason)
					continue
				}
				recycled++
				if !quiet {
					fmt.Fprintf(cmd.OutOrStdout(), "recycled %s\n", path)
				}
			}
		}
		if recycled > 0 {
			startDaemon(cmd.ErrOrStderr())
		}
		return errors.Join(errs...)
	},
}

func init() {
	trashCmd.Flags().BoolP("quiet", "q", false, "do not report what was recycled")
	rootCmd.AddCommand(trashCmd)
}
