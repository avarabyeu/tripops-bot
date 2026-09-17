// Command tripops is the TripOps backend and its operational tooling.
//
// One binary, several commands:
//
//	tripops serve             API, Telegram bot, scheduler and delivery worker
//	tripops migrate up        apply pending migrations
//	tripops migrate status    show what has and has not been applied
//	tripops bot set-webhook   point Telegram at this deployment
//	tripops version           build information
//
// A modular monolith is the right shape for this product. The modules are
// separated by package boundaries and talk through services, not HTTP, so the
// deployment story stays "one container plus a database" while the code stays
// splittable if it ever needs to be.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/avarabyeu/tripops-bot/internal/cli"
)

func main() {
	// Cancelled on SIGINT/SIGTERM; every long-running command watches it.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cli.New().Run(ctx, os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "tripops: %v\n", err)
		os.Exit(1)
	}
}
