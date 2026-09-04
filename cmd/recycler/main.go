// Command recycler moves files to the recycle bin, and lists or restores what
// is already in it, using the same commands on Linux, macOS and Windows. Run
// on a terminal with no arguments, it opens the bin in a browser instead.
//
// Nothing here deletes anything permanently: an item leaves the bin only by
// being restored.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := execute(os.Args, interactive()); err != nil {
		fmt.Fprintln(os.Stderr, "recycler:", err)
		os.Exit(1)
	}
}

// execute routes here rather than on the root command, which leaves cobra's handling of an unknown command intact.
func execute(args []string, terminal bool) error {
	if len(args) == 1 && terminal {
		return runTUI()
	}
	rootCmd.SetArgs(args[1:])
	return rootCmd.Execute()
}
