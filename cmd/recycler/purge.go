package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/recycler"
)

var purgeCmd = &cobra.Command{
	Use:   "purge <item>...",
	Short: "Permanently delete individual items from the recycle bin",
	Long: `Permanently delete the given items from the recycle bin.

An item is named by its ID from "recycler list", or by its original path or
file name when that matches exactly one item. This cannot be undone; use
"recycler empty" to purge everything at once.`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		items, err := resolveAll(args)
		if err != nil {
			return err
		}
		if yes, _ := cmd.Flags().GetBool("yes"); !yes {
			what := fmt.Sprintf("%d item(s)", len(items))
			if len(items) == 1 {
				what = items[0].String()
			}
			ok, err := confirm(cmd, fmt.Sprintf("Permanently delete %s? This cannot be undone.", what))
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintln(cmd.OutOrStdout(), "cancelled")
				return nil
			}
		}
		ids := make([]string, 0, len(items))
		for _, item := range items {
			ids = append(ids, item.ID)
		}
		if err := recycler.Purge(ids...); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "purged %d item(s)\n", len(ids))
		return nil
	},
}

func init() {
	purgeCmd.Flags().BoolP("yes", "y", false, "do not ask for confirmation")
	rootCmd.AddCommand(purgeCmd)
}
