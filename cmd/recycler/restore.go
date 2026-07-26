package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/recycler"
)

var restoreCmd = &cobra.Command{
	Use:   "restore <item>...",
	Short: "Move items out of the recycle bin, back where they came from",
	Long: `Restore each item to the location it was recycled from.

An item is named by its ID from "recycler list", or by its original path or
file name when that matches exactly one item. With --to, a single item is
restored to the given path instead of its original location.`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dest, _ := cmd.Flags().GetString("to")
		if dest != "" && len(args) != 1 {
			return errors.New("--to restores a single item, so it takes exactly one argument")
		}
		items, err := resolveAll(args)
		if err != nil {
			return err
		}
		var errs []error
		for _, item := range items {
			restored, err := recycler.RestoreTo(item.ID, dest)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			fmt.Fprintf(cmd.OutOrStdout(), "restored %s\n", restored)
		}
		return errors.Join(errs...)
	},
}

func init() {
	restoreCmd.Flags().String("to", "", "restore to this path instead of the original location")
	rootCmd.AddCommand(restoreCmd)
}
