package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/wow-look-at-my/recycler/internal/cargotarget"
)

// trimRootsEnv lists the directories to scan when the command is given none.
const trimRootsEnv = "RECYCLER_TRIM_ROOTS"

// reposDirName is the default scan root, relative to the home directory.
const reposDirName = "repos"

var trimCmd = &cobra.Command{
	Use:     "trim [directory...]",
	Aliases: []string{"cargo-clean"},
	Short:   "Keep cargo build directories inside a size cap",
	Long: `Recycle the oldest regenerable build output inside every cargo target directory.

rustc only ever adds to a target directory. Every branch, every worktree, every
dependency bump and every feature or profile permutation leaves its object
files, rlibs and fingerprints behind, and nothing takes any of it back: cargo has
no garbage collection. Its one remedy is "cargo clean", which throws away the
whole directory and rebuilds everything still wanted. Several agents each
building in their own worktree under .claude/worktrees or .grok/worktrees each
carry a target directory of their own on top of that.

This caps each target directory instead. It takes the oldest regenerable output
first, in this order, and stops as soon as the directory fits, so the build in
progress keeps its cache:

  1. cargo's incremental compilation cache, which puts no bound on how many
     generations of itself it keeps,
  2. the object files, rlibs, metadata, executables and .d files of each deps
     directory, least recently used first,
  3. the rebuild fingerprints, whose loss makes cargo rebuild the crate they name,
  4. for a worktree's target directory only, a whole build profile directory,
     which is what a merged or deleted branch leaves behind.

What a run takes from one target directory goes to the recycle bin as a single
item, a recycler-trim-<time> directory holding each entry at its own path, where
"recycler list" shows it and "recycler restore" puts it back. A recycle bin sits
on the filesystem it takes from, so the move frees nothing by itself. The bin
therefore keeps build output to the same cap: after the move, the build output
it holds past the cap goes back to the filesystem, oldest first. The
disk-pressure daemon holds the bin to that cap between runs too. The move into
the bin is a rename, so an entry goes however much it weighs.

With no arguments the directories listed in RECYCLER_TRIM_ROOTS are scanned,
defaulting to ~/repos. A scan never crosses onto another filesystem, never
follows a symlink, and never enters a build directory that is not a validated
cargo target: the name alone is not enough, since Maven, Make and a checkout's
own source all use it. A CARGO_TARGET_DIR is found by the same scan when it lands
under a scanned root and is named target; one renamed to something else is not
cargo's marker at a place cargo writes, so it is left alone.

` + cargotarget.EnvCapName + ` sets the cap for every run, and --cap overrides it for one.

A size is what the filesystem says an entry takes, not the length the file states.
On a compressed pool those differ by a multiple, and the pool is the thing being
capped.`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		rawCap, _ := cmd.Flags().GetString("cap")
		limit, err := cargotarget.ParseCap(rawCap)
		if err != nil {
			return err
		}
		if limit <= 0 {
			return fmt.Errorf("recycler: a size cap has to be more than zero: %q", rawCap)
		}
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		verbose, _ := cmd.Flags().GetBool("verbose")

		roots := args
		if len(roots) == 0 {
			if roots, err = trimRoots(); err != nil {
				return err
			}
		}

		out := cmd.OutOrStdout()
		var errs []error
		whole := &cargotarget.Outcome{}
		for _, root := range roots {
			outcome, err := cargotarget.Trim(root, cargotarget.Options{Cap: limit, DryRun: dryRun})
			if err != nil {
				errs = append(errs, err)
				continue
			}
			reportTrim(out, outcome, dryRun, verbose)
			whole.Plans = append(whole.Plans, outcome.Plans...)
			whole.Recycled += outcome.Recycled
			whole.Reclaimed += outcome.Reclaimed
			whole.Returned = append(whole.Returned, outcome.Returned...)
			whole.Refusals = append(whole.Refusals, outcome.Refusals...)
			whole.Errors = append(whole.Errors, outcome.Errors...)
		}
		reportTrimTotals(out, whole, dryRun)
		reportTrimRefusals(out, whole)
		for _, err := range whole.Errors {
			fmt.Fprintf(out, "trim failed: %v\n", err)
		}
		return errors.Join(errs...)
	},
}

// trimRoots returns the directories to scan: those RECYCLER_TRIM_ROOTS lists, or
// ~/repos. A machine with neither is told so rather than reported clean.
func trimRoots() ([]string, error) {
	if listed := os.Getenv(trimRootsEnv); listed != "" {
		return filepath.SplitList(listed), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(home, reposDirName)
	if _, err := os.Stat(root); err != nil {
		return nil, fmt.Errorf("recycler: no directory to scan: %s is not there, so name one or set %s",
			root, trimRootsEnv)
	}
	return []string{root}, nil
}

// reportTrim gives one run's account per target directory: what it holds, and
// what would go and in which order.
func reportTrim(out io.Writer, o *cargotarget.Outcome, dryRun, verbose bool) {
	for _, plan := range o.Plans {
		kind, entries, taken := "took", plan.Recycled, plan.Applied
		if dryRun {
			kind, entries, taken = "would take", len(plan.Recycle), plan.Reclaimed
		}
		fmt.Fprintf(out, "%s  %s, over a %s cap\n", plan.Target.Path, humanSize(plan.Size), humanSize(plan.Cap))
		if plan.Target.Worktree != "" {
			// The branch is what the reader acts on.
			fmt.Fprintf(out, "  worktree %s\n", plan.Target.Worktree)
		}

		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		for _, t := range tallyByTier(plan) {
			fmt.Fprintf(w, "\t%s\t%s\t%s\n", t.tier, humanSize(t.bytes), plural(t.count, "entry", "entries"))
		}
		_ = w.Flush()

		fmt.Fprintf(out, "  %s %s, %s, leaving %s\n",
			kind, plural(entries, "entry", "entries"),
			humanSize(taken), humanSize(plan.Size-taken))

		if verbose {
			for _, c := range plan.Recycle {
				fmt.Fprintf(out, "\t%s\t%s\t%s\n", c.Tier, humanSize(c.Size), c.Path)
			}
		}
		if short := plan.Shortfall(); short > 0 {
			fmt.Fprintf(out, "  still %s over the cap: nothing left to recycle is eligible here\n", humanSize(short))
		}
	}
}

// A tally is one tier's share of a plan.
type tally struct {
	tier  cargotarget.Tier
	count int
	bytes int64
}

// tallyByTier groups a plan by the kind of build output each entry is, in the
// order the entries go.
func tallyByTier(plan cargotarget.Plan) []tally {
	var tallies []tally
	index := map[cargotarget.Tier]int{}
	for _, c := range plan.Recycle {
		at, seen := index[c.Tier]
		if !seen {
			at = len(tallies)
			index[c.Tier] = at
			tallies = append(tallies, tally{tier: c.Tier})
		}
		tallies[at].count++
		tallies[at].bytes += c.Size
	}
	return tallies
}

// reportTrimTotals is the line a script reads: what the run found, and what
// it did about it.
func reportTrimTotals(out io.Writer, o *cargotarget.Outcome, dryRun bool) {
	if len(o.Returned) > 0 {
		fmt.Fprintf(out, "the bin gave back %s of build output past its budget, %s\n",
			humanSize(o.Freed()), plural(len(o.Returned), "item", "items"))
	}
	if len(o.Plans) == 0 {
		fmt.Fprintln(out, "nothing to do: every cargo target directory scanned is inside its cap")
		return
	}
	taken := o.Reclaimed
	entries := o.Recycled
	what := "trimmed"
	if dryRun {
		taken = o.Planned()
		entries, what = 0, "over the cap"
		for _, plan := range o.Plans {
			entries += len(plan.Recycle)
		}
	}
	fmt.Fprintf(out, "%s %s, %s taking %s\n",
		plural(len(o.Plans), "target directory", "target directories"), what,
		plural(entries, "entry", "entries"), humanSize(taken))
}

// reportTrimRefusals names what the run left over the cap. A refusal is the
// finding, not a detail: it is the size the cap could not reach.
func reportTrimRefusals(out io.Writer, o *cargotarget.Outcome) {
	for _, r := range o.Refusals {
		fmt.Fprintf(out, "refused %s: %s\n", r.Path, r.Reason)
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func init() {
	trimCmd.Flags().String("cap", capFlagDefault(),
		"bytes each target directory may hold, with an optional B, K, M, G or T suffix; "+cargotarget.EnvCapName+" sets it for every run")
	trimCmd.Flags().Bool("dry-run", false, "report what would be recycled and what it weighs, and change nothing")
	trimCmd.Flags().BoolP("verbose", "v", false, "name every entry, not the total per kind")
	rootCmd.AddCommand(trimCmd)
}

// capFlagDefault spells the cap this run starts from, which the environment can
// already have changed.
func capFlagDefault() string {
	const unit = 1 << 30
	cap := cargotarget.CapFromEnv(cargotarget.DefaultCap)
	if cap%unit == 0 {
		return fmt.Sprintf("%dG", cap/unit)
	}
	return fmt.Sprint(cap)
}
