// empty.go holds emptyCriteria.

package main

import (
	"bufio"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/recycler"
	"github.com/wow-look-at-my/recycler/internal/pressure"
)

var emptyCmd = &cobra.Command{
	Use:   "empty",
	Short: "Permanently delete selected items from the recycle bin",
	Long: `Permanently delete the items the selectors pick, oldest first.

  --older-than 30d   every item recycled longer ago than this (at least 1d)
  --min-free 20GB    the oldest items on each filesystem holding a bin, until
                     it has this much free (or a percentage, at most 50%)
  --max-size 50GB    the oldest items, until the whole bin is at most this
                     (at least 1 GiB)

An item goes when any selector picks it. Nothing recycled in the last day goes,
whatever the selectors say, so no selector can empty the whole bin.

This lists what it would delete and asks first. --dry-run only lists it, and
--yes skips the question. An AI agent session (one with CLAUDECODE, GROK_BUILD
or a like variable set) cannot empty the bin.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if name, ok := recycler.AgentSession(); ok {
			return fmt.Errorf("%w: %s is set, so this looks like an AI agent session, and only a person may empty the recycle bin",
				recycler.ErrAgentSession, name)
		}
		c, err := emptyCriteria(cmd)
		if err != nil {
			return err
		}
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		yes, _ := cmd.Flags().GetBool("yes")
		out := cmd.OutOrStdout()

		plan, err := recycler.Empty(c, true)
		if err != nil {
			return err
		}
		for _, line := range plan.Unmet {
			fmt.Fprintln(cmd.ErrOrStderr(), line)
		}
		if len(plan.Evicted) == 0 {
			fmt.Fprintln(out, "nothing to empty")
			return nil
		}
		var weight int64
		for _, ev := range plan.Evicted {
			fmt.Fprintf(out, "would delete %s\n", describeEviction(ev))
			if ev.Item.Size > 0 {
				weight += ev.Item.Size
			}
		}
		summary := fmt.Sprintf("%d items, %s", len(plan.Evicted), pressure.Bytes(uint64(weight)))
		if dryRun {
			fmt.Fprintf(out, "%s would be deleted permanently\n", summary)
			return nil
		}
		if !yes && !confirm(cmd, "permanently delete "+summary+"? [y/N] ") {
			return errors.New("not confirmed; nothing was deleted")
		}

		done, err := recycler.Empty(c, false)
		if err != nil {
			return err
		}
		failed := 0
		for _, ev := range done.Evicted {
			if ev.Error != nil {
				failed++
				fmt.Fprintf(cmd.ErrOrStderr(), "could not delete %s: %v\n", ev.Item.Name, ev.Error)
				continue
			}
			fmt.Fprintf(out, "deleted %s\n", describeEviction(ev))
		}
		if failed > 0 {
			return fmt.Errorf("%d items could not be deleted", failed)
		}
		return nil
	},
}

// emptyCriteria reads the selector flags. The library checks the bounds.
func emptyCriteria(cmd *cobra.Command) (recycler.EmptyCriteria, error) {
	var c recycler.EmptyCriteria
	if v, _ := cmd.Flags().GetString("older-than"); v != "" {
		age, err := parseAge(v)
		if err != nil {
			return c, err
		}
		c.OlderThan = age
	}
	if v, _ := cmd.Flags().GetString("min-free"); v != "" {
		a, err := recycler.ParseAmount(v)
		if err != nil {
			return c, err
		}
		c.MinFree = a
	}
	if v, _ := cmd.Flags().GetString("max-size"); v != "" {
		a, err := recycler.ParseAmount(v)
		if err != nil {
			return c, err
		}
		if a.Percent > 0 {
			return c, errors.New("--max-size takes a size such as 50GB, not a percentage")
		}
		c.MaxSize = a.Bytes
	}
	return c, c.Validate()
}

// parseAge reads "30d", "2w" or anything time.ParseDuration takes.
func parseAge(s string) (time.Duration, error) {
	for suffix, unit := range map[string]time.Duration{"d": 24 * time.Hour, "w": 7 * 24 * time.Hour} {
		if n, ok := strings.CutSuffix(s, suffix); ok {
			v, err := strconv.ParseFloat(n, 64)
			if err != nil || v <= 0 {
				return 0, fmt.Errorf("%q is not an age such as 30d, 2w or 36h", s)
			}
			return time.Duration(v * float64(unit)), nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%q is not an age such as 30d, 2w or 36h", s)
	}
	return d, nil
}

// confirm asks on standard error and reads one answer from standard input.
// Anything but y or yes, an end of input included, is no.
func confirm(cmd *cobra.Command, question string) bool {
	fmt.Fprint(cmd.ErrOrStderr(), question)
	answer, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	}
	return false
}

func init() {
	emptyCmd.Flags().String("older-than", "", "delete items recycled longer ago than this, such as 30d")
	emptyCmd.Flags().String("min-free", "", "delete the oldest items until each filesystem has this free, such as 20GB or 15%")
	emptyCmd.Flags().String("max-size", "", "delete the oldest items until the bin is at most this, such as 50GB")
	emptyCmd.Flags().Bool("dry-run", false, "list what would be deleted, and delete nothing")
	emptyCmd.Flags().BoolP("yes", "y", false, "do not ask before deleting")
	rootCmd.AddCommand(emptyCmd)
}
