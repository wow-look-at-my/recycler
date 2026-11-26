// One call for every path, so the backend can batch its bookkeeping.

package main

import (
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
daemon keeps free, or when a path is larger than what is left, the bin first
gives back its oldest items there to make room. A path is deleted outright only
when everything the bin holds there cannot make that room. Saying either is not
optional: the lines naming what went go to standard error and --quiet does not
suppress them.`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		quiet, _ := cmd.Flags().GetBool("quiet")
		// One call for every path, so the backend can batch its bookkeeping.
		disposals, err := recycler.Recycle(args...)
		recycled := 0
		for _, d := range disposals {
			for _, it := range d.Evicted {
				// Never quiet either: these left the bin for good.
				fmt.Fprintf(cmd.ErrOrStderr(), "evicted %s to make room for %s: %s\n",
					describeEviction(recycler.Eviction{Item: it}), d.Path, d.Reason)
			}
			if d.Permanent {
				// Never quiet. A caller who believes this went to the bin
				// will go looking for it, and there is nothing to find.
				fmt.Fprintf(cmd.ErrOrStderr(), "deleted %s permanently, not recycled: %s\n",
					d.Path, d.Reason)
				continue
			}
			recycled++
			if !quiet {
				fmt.Fprintf(cmd.OutOrStdout(), "recycled %s\n", d.Path)
			}
		}
		if recycled > 0 {
			startDaemon(cmd.ErrOrStderr())
		}
		return err
	},
}

func init() {
	trashCmd.Flags().BoolP("quiet", "q", false, "do not report what was recycled")
	rootCmd.AddCommand(trashCmd)
}
