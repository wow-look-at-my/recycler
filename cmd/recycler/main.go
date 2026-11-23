// execute routes here rather than on the root command, which leaves cobra's handling of an unknown command intact.

package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	if err := execute(os.Args, interactive()); err != nil {
		fmt.Fprintln(os.Stderr, errorLine(err))
		os.Exit(1)
	}
}

// errorLine prefixes an error with the program name, once. Library errors
// carry the prefix already.
func errorLine(err error) string {
	if msg := err.Error(); strings.HasPrefix(msg, "recycler: ") {
		return msg
	}
	return "recycler: " + err.Error()
}

// execute routes here rather than on the root command, which leaves cobra's handling of an unknown command intact.
func execute(args []string, terminal bool) error {
	if len(args) == 1 && terminal {
		return runTUI()
	}
	rootCmd.SetArgs(args[1:])
	return rootCmd.Execute()
}
