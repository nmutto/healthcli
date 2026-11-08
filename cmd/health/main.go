// Command health is a local-first CLI for Apple Health data.
// All the real work lives in internal/cli; main stays thin on purpose.
package main

import (
	"os"

	"github.com/muttoni/health/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
