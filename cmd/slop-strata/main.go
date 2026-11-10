// Command slop-strata scores code quality on a change, a snapshot, and a
// repository's history.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
)

// Set by goreleaser through -X main.<name>.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// exitUsage is the exit status for a bad command line.
const exitUsage = 2

func main() {
	os.Exit(runMain())
}

func runMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "slop-strata:", err)
		if errors.Is(err, errUsage) {
			fmt.Fprintln(os.Stderr, `Run "slop-strata --help" for usage.`)

			return exitUsage
		}

		return 1
	}

	return 0
}
