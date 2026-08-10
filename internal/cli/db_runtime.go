package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/larchwave/flowbaton/internal/sessionstore"
)

type DBRunner struct {
	Migrate func(context.Context, string) error
}

func (runner DBRunner) Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) != 3 || args[0] != "migrate" || args[1] != "--database-url" || args[2] == "" {
		fmt.Fprintln(stderr, "usage: flowbaton db migrate --database-url URL")
		return ExitInvalid
	}
	migrate := runner.Migrate
	if migrate == nil {
		migrate = migratePostgres
	}
	if err := migrate(ctx, args[2]); err != nil {
		fmt.Fprintf(stderr, "db migrate: %v\n", err)
		return ExitFailure
	}
	fmt.Fprintln(stdout, "database schema is current")
	return ExitOK
}

func migratePostgres(ctx context.Context, databaseURL string) error {
	store, err := sessionstore.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	return store.Migrate(ctx)
}
