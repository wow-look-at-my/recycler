package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/recycler"
)

var emptyCmd = &cobra.Command{
	Use:   "empty",
	Short: "Permanently delete everything in the recycle bin",
	Long: `Empty the current user's recycle bin.

Everything in it is destroyed and cannot be restored afterwards.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		items, err := recycler.List()
		if err != nil {
			return err
		}
		if len(items) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "the recycle bin is already empty")
			return nil
		}
		if yes, _ := cmd.Flags().GetBool("yes"); !yes {
			ok, err := confirm(cmd, fmt.Sprintf("Permanently delete all %d item(s) in the recycle bin? This cannot be undone.", len(items)))
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintln(cmd.OutOrStdout(), "cancelled")
				return nil
			}
		}
		if err := recycler.Empty(); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "the recycle bin is now empty")
		return nil
	},
}

func init() {
	emptyCmd.Flags().BoolP("yes", "y", false, "do not ask for confirmation")
	rootCmd.AddCommand(emptyCmd)
}
