package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/larchwave/flowbaton/internal/sessionstore"
)

type DBRunner struct {
	ApplySchema func(context.Context, string) error
}

func (runner DBRunner) Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) != 3 || args[0] != "apply-schema" || args[1] != "--database-url" || args[2] == "" {
		fmt.Fprintln(stderr, "usage: flowbaton db apply-schema --database-url URL")
		return ExitInvalid
	}
	apply := runner.ApplySchema
	if apply == nil {
		apply = applyPostgresSchema
	}
	if err := apply(ctx, args[2]); err != nil {
		fmt.Fprintf(stderr, "db apply-schema: %v\n", err)
		return ExitFailure
	}
	fmt.Fprintln(stdout, "database schema is current")
	return ExitOK
}

func applyPostgresSchema(ctx context.Context, databaseURL string) error {
	store, err := sessionstore.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	return store.ApplySchema(ctx)
}
