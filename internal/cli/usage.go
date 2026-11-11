package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// argHelp says how many arguments a command needs and what to tell the user
// when some are missing.
type argHelp struct {
	min  int
	hint string
}

var argHelps = map[string]argHelp{
	"trend":        {1, "trend needs a metric, e.g. healthcli trend steps"},
	"log":          {2, "log needs a metric and a value, e.g. healthcli log weight 72.5"},
	"query":        {1, `query needs one SQL statement, e.g. healthcli query "SELECT count(*) FROM samples"`},
	"import apple": {1, "import apple needs the export file, e.g. healthcli import apple export.zip"},
}

// wireUsage replaces cobra's terse argument and flag errors ("accepts 1
// arg(s), received 0") with messages that say what to type. Both kinds of
// mistake become usage errors (exit code 2). It walks the finished command
// tree, so the command files stay as they are.
func wireUsage(root *cobra.Command) {
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return usageErrorf("%v; run \"%s --help\" to see the flags", err, cmd.CommandPath())
	})
	walk(root, func(cmd *cobra.Command) {
		if cmd.Args == nil || !cmd.Runnable() {
			return
		}
		check := cmd.Args
		cmd.Args = func(c *cobra.Command, args []string) error {
			if check(c, args) == nil {
				return nil
			}
			return usageErrorf("%s", argMessage(c, args))
		}
	})
}

// argMessage explains a wrong number of arguments.
func argMessage(cmd *cobra.Command, args []string) string {
	path := strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()+" ")
	if path == "log" && cmd.Flags().Changed("delete") {
		return "log --delete takes one metric and --at, e.g. healthcli log --delete hr --at 2025-03-01T08:30"
	}
	if h, ok := argHelps[path]; ok && len(args) < h.min {
		return h.hint
	}
	help := fmt.Sprintf("run \"%s --help\" for how to use it", cmd.CommandPath())
	if len(args) == 0 {
		return fmt.Sprintf("%s is missing an argument; %s", path, help)
	}
	if cmd.Args != nil && cobraNoArgs(cmd) {
		return fmt.Sprintf("%s takes no arguments, got %q; %s", path, args[0], help)
	}
	return fmt.Sprintf("%s got too many arguments (%d); %s", path, len(args), help)
}

// cobraNoArgs reports whether a command is declared without positional
// arguments, by its Use line having no "<" or "[".
func cobraNoArgs(cmd *cobra.Command) bool {
	return !strings.ContainsAny(cmd.Use, "<[")
}

// walk calls f for the command and every command below it.
func walk(cmd *cobra.Command, f func(*cobra.Command)) {
	f(cmd)
	for _, sub := range cmd.Commands() {
		walk(sub, f)
	}
}
