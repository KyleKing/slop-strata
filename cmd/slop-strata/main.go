// Command slop-strata scores code quality on a change, a snapshot, and a
// repository's history.
package main

import (
	"context"
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

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-v", "--version":
			fmt.Printf("slop-strata %s (commit: %s, built: %s)\n", version, commit, date)

			return
		}
	}

	os.Exit(runMain())
}

func runMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "slop-strata:", err)

		return 1
	}

	return 0
}
