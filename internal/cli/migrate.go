package cli

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/avarabyeu/tripops-bot/migrations"
)

func migrateCommand() *cli.Command {
	return &cli.Command{
		Name:  "migrate",
		Usage: "Manage the database schema",
		Commands: []*cli.Command{
			{
				Name:   "up",
				Usage:  "Apply every pending migration",
				Action: runMigrateUp,
			},
			{
				Name:   "status",
				Usage:  "Show the current schema version and what is pending",
				Action: runMigrateStatus,
			},
			{
				Name:  "down",
				Usage: "Roll migrations back",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:  "steps",
						Usage: "how many migrations to undo; 0 means all of them",
						Value: 1,
					},
					&cli.BoolFlag{
						Name:  "yes",
						Usage: "confirm: rolling back drops data",
					},
				},
				Action: runMigrateDown,
			},
			{
				Name:      "force",
				Usage:     "Mark the schema as being at VERSION without running anything",
				ArgsUsage: "VERSION",
				Description: "Escape hatch for a migration that failed halfway and left the\n" +
					"   version marked dirty. Fix the database by hand first, then force.",
				Action: runMigrateForce,
			},
		},
	}
}

func runMigrateUp(ctx context.Context, cmd *cli.Command) error {
	application, closeDB, err := boot(ctx, cmd)
	if err != nil {
		return err
	}
	defer closeDB()

	if err := migrations.Run(ctx, application.database, application.log); err != nil {
		return err
	}
	state, err := migrations.Status(application.database)
	if err != nil {
		return err
	}
	if len(state.Pending) > 0 {
		return errUsage("migrations still pending after running: %v", state.Pending)
	}
	fmt.Printf("schema is up to date at version %d\n", state.Version)
	return nil
}

func runMigrateStatus(ctx context.Context, cmd *cli.Command) error {
	application, closeDB, err := boot(ctx, cmd)
	if err != nil {
		return err
	}
	defer closeDB()

	state, err := migrations.Status(application.database)
	if err != nil {
		return err
	}
	fmt.Printf("engine:  %s\nversion: %d\n", state.Engine, state.Version)
	if state.Dirty {
		fmt.Println("state:   DIRTY — a migration failed partway; fix it and run `migrate force`")
	}
	fmt.Println()

	if len(state.Applied) == 0 {
		fmt.Println("applied: none")
	} else {
		fmt.Println("applied:")
		for _, name := range state.Applied {
			fmt.Printf("  ✓ %s\n", name)
		}
	}
	if len(state.Pending) == 0 {
		fmt.Println("pending: none")
		return nil
	}
	fmt.Println("pending:")
	for _, name := range state.Pending {
		fmt.Printf("  · %s\n", name)
	}
	return nil
}

func runMigrateDown(ctx context.Context, cmd *cli.Command) error {
	if !cmd.Bool("yes") {
		return errUsage("rolling back drops data; re-run with --yes to confirm")
	}
	application, closeDB, err := boot(ctx, cmd)
	if err != nil {
		return err
	}
	defer closeDB()

	steps := cmd.Int("steps")
	if err := migrations.Down(ctx, application.database, steps, application.log); err != nil {
		return err
	}
	state, err := migrations.Status(application.database)
	if err != nil {
		return err
	}
	fmt.Printf("rolled back to version %d\n", state.Version)
	return nil
}

func runMigrateForce(ctx context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() != 1 {
		return errUsage("pass the version to force, e.g. `tripops migrate force 1`")
	}
	var version int
	if _, err := fmt.Sscanf(cmd.Args().First(), "%d", &version); err != nil {
		return errUsage("%q is not a version number", cmd.Args().First())
	}

	application, closeDB, err := boot(ctx, cmd)
	if err != nil {
		return err
	}
	defer closeDB()

	if err := migrations.Force(ctx, application.database, version, application.log); err != nil {
		return err
	}
	fmt.Printf("schema version forced to %d\n", version)
	return nil
}
