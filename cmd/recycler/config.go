package main

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/recycler"
	"github.com/wow-look-at-my/recycler/internal/pressure"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Show or change how much free space the daemon keeps",
	Long: `Show the daemon settings, and what they mean on each filesystem holding a bin.

  min-free   free space the daemon keeps on each filesystem holding a bin,
             as 20GB, 5GiB or 15%. The built-in floor is a tenth of the
             filesystem, at most 1 GiB, and no setting goes under it: below
             it a recycle deletes instead. No setting goes over half of the
             filesystem either, or the daemon would chase it by giving back
             every recycled item.
  verbose    true makes the daemon log each sweep and why each item went.

A running daemon reads a change on its next tick. An AI agent session (one with
CLAUDECODE, GROK_BUILD or a like variable set) may read the settings but not
change them.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := recycler.LoadDaemonSettings()
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		minFree := s.MinFree
		if minFree == "" {
			minFree = "built-in (a tenth of each filesystem, at most 1 GiB)"
		}
		fmt.Fprintf(out, "min-free: %s\n", minFree)
		fmt.Fprintf(out, "verbose: %t\n", s.Verbose)
		if path, err := recycler.DaemonSettingsPath(); err == nil {
			fmt.Fprintf(out, "file: %s\n", path)
		}
		targets, err := recycler.DaemonTargets(s)
		if err != nil {
			return err
		}
		for _, t := range targets {
			fmt.Fprintf(out, "%s: %s free of %s; sweeps under %s free, up to %s\n", t.Dir,
				pressure.Bytes(t.Avail), pressure.Bytes(t.Total), pressure.Bytes(t.Trigger), pressure.Bytes(t.Recover))
		}
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <min-free|verbose> <value>",
	Short: "Change a daemon setting",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return changeSetting(cmd, args[0], args[1], true)
	},
}

var configUnsetCmd = &cobra.Command{
	Use:   "unset <min-free|verbose>",
	Short: "Put a daemon setting back to its built-in value",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return changeSetting(cmd, args[0], "", false)
	},
}

func changeSetting(cmd *cobra.Command, key, value string, set bool) error {
	s, err := recycler.LoadDaemonSettings()
	if err != nil {
		return err
	}
	switch key {
	case "min-free":
		if set {
			if _, err := recycler.ParseAmount(value); err != nil {
				return err
			}
		}
		s.MinFree = value
	case "verbose":
		s.Verbose = false
		if set {
			if s.Verbose, err = strconv.ParseBool(value); err != nil {
				return fmt.Errorf("verbose takes true or false, not %q", value)
			}
		}
	default:
		return fmt.Errorf("unknown setting %q: the settings are min-free and verbose", key)
	}
	if err := recycler.SaveDaemonSettings(s); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "saved; a running daemon reads it on its next tick")
	return nil
}

func init() {
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configUnsetCmd)
	rootCmd.AddCommand(configCmd)
}
