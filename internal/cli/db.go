package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/avarabyeu/tripops-bot/internal/db"
)

func dbCommand() *cli.Command {
	return &cli.Command{
		Name:  "db",
		Usage: "Database maintenance",
		Commands: []*cli.Command{
			{
				Name:      "backup",
				Usage:     "Write a consistent snapshot of the database to a file or to stdout",
				ArgsUsage: "PATH",
				Description: "Uses SQLite's VACUUM INTO, which is safe to run while the\n" +
					"   application is serving requests. Copying the database file with cp\n" +
					"   is not: it can catch a write in progress and produce a backup that\n" +
					"   looks fine and will not open.\n\n" +
					"   With --stdout the snapshot is streamed out and nothing is left\n" +
					"   behind, which is what a remote backup wants: the runtime image has\n" +
					"   no shell to tidy up with.",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "stdout",
						Usage: "stream the snapshot to stdout instead of writing a file",
					},
				},
				Action: runDBBackup,
			},
		},
	}
}

func runDBBackup(ctx context.Context, cmd *cli.Command) error {
	toStdout := cmd.Bool("stdout")
	if toStdout && cmd.Args().Len() > 0 {
		return errUsage("--stdout writes the snapshot to stdout; do not also pass a path")
	}
	if !toStdout && cmd.Args().Len() != 1 {
		return errUsage("pass where to write the backup, e.g. `tripops db backup /backups/tripops.db`")
	}

	application, closeDB, err := boot(ctx, cmd)
	if err != nil {
		return err
	}
	defer closeDB()

	// VACUUM INTO is SQLite's. PostgreSQL has its own, much better, tooling.
	if engine := application.database.Dialect(); engine != db.SQLite {
		return errUsage("db backup only works on SQLite; this is %s, so use pg_dump", engine)
	}

	if toStdout {
		return streamBackup(ctx, application)
	}

	target, err := filepath.Abs(cmd.Args().First())
	if err != nil {
		return errUsage("%q is not a usable path: %v", cmd.Args().First(), err)
	}
	// VACUUM INTO refuses to overwrite, and silently clobbering a previous
	// backup would be worse anyway.
	if _, err := os.Stat(target); err == nil {
		return errUsage("%s already exists; pick a new path or move it aside", target)
	}
	if err := snapshot(ctx, application, target); err != nil {
		return err
	}

	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("db backup: the snapshot was not written: %w", err)
	}
	// The size is here so a truncated or empty backup is obvious at a glance.
	fmt.Printf("wrote %s (%.1f MiB)\n", target, float64(info.Size())/(1<<20))
	return nil
}

// streamBackup snapshots to a temporary file and copies it to stdout.
//
// VACUUM INTO needs a path, so a temporary file is unavoidable. It is created
// beside the database, the one directory certain to be writable, and removed
// here rather than by a shell the runtime image does not have.
func streamBackup(ctx context.Context, application *app) error {
	dir := filepath.Dir(db.SQLiteFilePath(application.database.URL()))
	target := filepath.Join(dir, fmt.Sprintf(".backup-%d.db", time.Now().UnixNano()))
	defer func() {
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			application.log.Warn("could not remove the temporary snapshot",
				"path", target, "err", err)
		}
	}()

	if err := snapshot(ctx, application, target); err != nil {
		return err
	}
	file, err := os.Open(target)
	if err != nil {
		return fmt.Errorf("db backup: reopen snapshot: %w", err)
	}
	defer func() { _ = file.Close() }()

	if _, err := io.Copy(os.Stdout, file); err != nil {
		return fmt.Errorf("db backup: stream snapshot: %w", err)
	}
	return nil
}

func snapshot(ctx context.Context, application *app, target string) error {
	if err := application.database.WithContext(ctx).Exec("VACUUM INTO ?", target).Error; err != nil {
		return fmt.Errorf("db backup: %w", err)
	}
	return nil
}
