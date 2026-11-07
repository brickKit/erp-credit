package limits

import (
	"context"

	core "github.com/brickKit/erp-credit/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/db"
)

// The order exposure of a limit is written only here (the limit row locked by the caller, protocol 04's resource
// lock): a hold takes its amount, a consumption takes off what it consumed (to the receivable side later, protocol
// 05), a release or an expiry gives back what the branch had not consumed. Each raises the limit's version and
// publishes the limit in the same transaction.

// Lock locks limit id: a branch's resource, before the branch (be-protocol P16.11).
func Lock(ctx context.Context, tx db.Tx, id string) error {
	_, err := core.GetLimit(ctx, tx, id, true)
	return err
}

// TakeOrder adds a held amount to the order exposure.
func TakeOrder(m *core.Base, ctx context.Context, tx db.Tx, id, amount string) error {
	return moveOrder(m, ctx, tx, id, `order_exposure + $2::numeric`, amount)
}

// ConsumeOrder takes a consumed amount off the order exposure.
func ConsumeOrder(m *core.Base, ctx context.Context, tx db.Tx, id, amount string) error {
	return moveOrder(m, ctx, tx, id, `order_exposure - $2::numeric`, amount)
}

// GiveBackOrder takes off the order exposure what a branch of amount had not consumed.
func GiveBackOrder(m *core.Base, ctx context.Context, tx db.Tx, id, amount, consumed string) error {
	return moveOrder(m, ctx, tx, id, `order_exposure - ($2::numeric - $3::numeric)`, amount, consumed)
}

func moveOrder(m *core.Base, ctx context.Context, tx db.Tx, id, to string, args ...any) error {
	if _, err := tx.Exec(ctx, `UPDATE credit_limit SET order_exposure = `+to+`, version = version + 1, updated_at = now()
		WHERE id = $1`, append([]any{id}, args...)...); err != nil {
		return err
	}
	_, err := core.EmitLimit(m, ctx, tx, id)
	return err
}
