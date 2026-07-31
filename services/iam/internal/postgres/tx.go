package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (d *Database) WithinTransaction(
	ctx context.Context,
	fn func(context.Context, pgx.Tx) error,
) error {
	tx, err := d.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
