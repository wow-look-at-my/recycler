package main

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/recycler"
)

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List what is in the recycle bin",
	Long: `List the current user's recycle bin, newest first.

The ID column is what restore takes as its argument, though a name or original
path is accepted too when it matches exactly one item.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		items, err := recycler.List()
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()

		if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
			if items == nil {
				items = []recycler.Item{}
			}
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(items)
		}

		if len(items) == 0 {
			fmt.Fprintln(out, "the recycle bin is empty")
			return nil
		}
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "DELETED\tSIZE\tORIGINAL PATH\tID")
		for _, item := range items {
			origin := item.OriginalPath
			if origin == "" {
				origin = item.Name + "  (original location unknown)"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				item.DeletedAt.Local().Format(time.RFC3339), humanSize(item.Size), origin, item.ID)
		}
		return w.Flush()
	},
}

func init() {
	listCmd.Flags().Bool("json", false, "print the listing as JSON")
	rootCmd.AddCommand(listCmd)
}

// humanSize renders a byte count in units a person can read at a glance.
func humanSize(size int64) string {
	if size < 0 {
		return "?"
	}
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTP"[exp])
}
