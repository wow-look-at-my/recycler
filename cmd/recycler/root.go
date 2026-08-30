package main

import (
	"github.com/spf13/cobra"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

var rootCmd = &cobra.Command{
	Use:   "recycler",
	Short: "Work with the recycle bin on Linux, macOS and Windows",
	Long: `recycler moves files to the recycle bin instead of destroying them, and
lists or restores what is already in there.

Nothing here deletes anything permanently: an item leaves the bin only by being
restored. Emptying the bin is left to the desktop environment.

The same commands work on every platform: the FreeDesktop trash can on Linux,
the Trash on macOS and the Recycle Bin on Windows.`,
	Version:       version,
	SilenceUsage:  true,
	SilenceErrors: true,
}
