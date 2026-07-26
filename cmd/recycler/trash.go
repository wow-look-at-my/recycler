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
without stopping the remaining ones.`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		quiet, _ := cmd.Flags().GetBool("quiet")
		// One call per path, so each success can be reported as it happens
		// and a failure only affects its own path.
		var errs []error
		for _, path := range args {
			if err := recycler.Recycle(path); err != nil {
				errs = append(errs, err)
				continue
			}
			if !quiet {
				fmt.Fprintf(cmd.OutOrStdout(), "recycled %s\n", path)
			}
		}
		return errors.Join(errs...)
	},
}

func init() {
	trashCmd.Flags().BoolP("quiet", "q", false, "do not report what was recycled")
	rootCmd.AddCommand(trashCmd)
}
