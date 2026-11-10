package cli

import (
	"context"
	"time"

	"github.com/spf13/cobra"
)

// nowFunc is the clock. Tests replace it so "today" is a fixed date.
var nowFunc = time.Now

// cmdContext returns the command's context, or a background one if cobra
// was started without any (older cobra versions leave it nil).
func cmdContext(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}
