package main

import (
	"embed"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/recycler"
	"github.com/wow-look-at-my/tml"
)

//go:embed ui
var uiFS embed.FS

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Browse, sort, filter and manage the recycle bin on a full screen",
	Long: `Open the recycle bin in a terminal interface.

Pick an item with the arrows or a click. Restore it with enter, restore it
somewhere else with t, or delete it permanently with d. Every action asks
first. Space marks items, a marks every listed item, and an action then takes
every marked item. s and S change the sort, and a click on a heading sorts on
that column. / filters by name or path. A right click, or m, opens a menu of
what can be done to the item. The wheel and the scrollbar scroll.

Running "recycler" with no arguments on a terminal opens the same interface.
An AI agent session cannot delete from it.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error { return runTUI() },
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}

func runTUI() error {
	if !recycler.Available() {
		return recycler.ErrUnsupported
	}
	m, err := newModel()
	if err != nil {
		return err
	}
	return tml.Run(m.view, m)
}
