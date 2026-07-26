// Command recycler moves files to the recycle bin, and lists, restores or
// permanently deletes what is already in it, using the same commands on Linux,
// macOS and Windows.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "recycler:", err)
		os.Exit(1)
	}
}
