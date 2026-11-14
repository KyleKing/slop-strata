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

// Exit statuses: 1 is only ever --exit-code reporting a change worth a look.
const (
	exitLook  = 1
	exitError = 2
)

func main() {
	os.Exit(runMain())
}

func runMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errWorthALook):
		return exitLook
	}
	fmt.Fprintln(os.Stderr, "slop-strata:", err)
	if errors.Is(err, errUsage) {
		fmt.Fprintln(os.Stderr, `Run "slop-strata --help" for usage.`)
	}

	return exitError
}
