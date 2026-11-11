package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/nmutto/healthcli/internal/metrics"
	"github.com/nmutto/healthcli/internal/stats"
)

// wireCompletion adds shell completion for metric names and for flags that
// take one of a few fixed words. It walks the finished command tree, so the
// command files stay as they are.
func wireCompletion(root *cobra.Command) {
	for _, cmd := range root.Commands() {
		switch cmd.Name() {
		case "trend", "heatmap":
			cmd.ValidArgsFunction = metricArg(0)
		case "log":
			cmd.ValidArgsFunction = metricArg(0) // the second argument is a number
		case "overview":
			setListFlag(cmd, "category", categoryNames())
			setListFlag(cmd, "heatmap", append([]string{"steps", "sleep", "workouts"}, metrics.Names()...))
		case "today":
			setListFlag(cmd, "category", categoryNames())
		case "sleep":
			setWordFlag(cmd, "by", []string{stats.SleepByAsleep, stats.SleepByBedtime, stats.SleepByEfficiency})
		case "workouts":
			setWordFlag(cmd, "by", []string{"type"})
		}
	}
}

// metricArg completes the metric name when it is the argument at position
// index, and nothing else.
func metricArg(index int) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != index {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return matching(metrics.Names(), toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

// setWordFlag completes a flag that takes exactly one of the given words.
func setWordFlag(cmd *cobra.Command, flag string, words []string) {
	cmd.RegisterFlagCompletionFunc(flag, func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return matching(words, toComplete), cobra.ShellCompDirectiveNoFileComp
	})
}

// setListFlag completes a flag that takes a comma list ("vitals,sleep"). The
// words before the last comma are kept, and NoSpace lets you type the next one.
func setListFlag(cmd *cobra.Command, flag string, words []string) {
	cmd.RegisterFlagCompletionFunc(flag, func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		head := ""
		if i := strings.LastIndex(toComplete, ","); i >= 0 {
			head, toComplete = toComplete[:i+1], toComplete[i+1:]
		}
		var out []string
		for _, w := range matching(words, toComplete) {
			out = append(out, head+w)
		}
		return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
	})
}

// matching keeps the words that start with prefix.
func matching(words []string, prefix string) []string {
	var out []string
	for _, w := range words {
		if strings.HasPrefix(w, prefix) {
			out = append(out, w)
		}
	}
	return out
}
