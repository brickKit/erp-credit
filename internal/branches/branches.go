package branches

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	core "github.com/brickKit/erp-credit/v3/internal/core"
	limits "github.com/brickKit/erp-credit/v3/internal/limits"

	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/errs"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/reserve"
	"github.com/brickKit/be-sdk-go/v3/work"
	creditv3 "github.com/brickKit/erp-credit/contract/v3/gen/go/be/erp/credit/v3"
	"github.com/jackc/pgx/v5"
)

// The branch states of protocol 04 and their wire values.
var branchStates = map[string]creditv3.BranchState{
	"held": creditv3.BranchState_BRANCH_STATE_HELD, "committed": creditv3.BranchState_BRANCH_STATE_COMMITTED,
	"consumed": creditv3.BranchState_BRANCH_STATE_CONSUMED, "released": creditv3.BranchState_BRANCH_STATE_RELEASED,
	"expired": creditv3.BranchState_BRANCH_STATE_EXPIRED, "rejected": creditv3.BranchState_BRANCH_STATE_REJECTED,
	"cancelled": creditv3.BranchState_BRANCH_STATE_CANCELLED,
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func wire(b core.Branch) *creditv3.Branch {
	return &creditv3.Branch{BranchId: b.ID, State: branchStates[b.State], Reason: str(b.Reason), PartnerId: str(b.PartnerID),
		LegalEntityId: str(b.LegalEntityID), Currency: str(b.Currency), Amount: str(b.Amount), Consumed: str(b.Consumed),
		Available: str(b.Available), HoldExpiresAt: str(b.HoldExpiresAt), Version: b.Version}
}

func branchID(id string) error {
	if !core.UUIDForm.MatchString(id) {
		return core.Invalid("branch_id", "branch_id is the coordinator's UUID for the branch")
	}
	return nil
}

func holdContent(in *creditv3.HoldRequest, amt string) string {
	sum := sha256.Sum256([]byte(fmt.Sprint(in.GetPartnerId(), "|", in.GetLegalEntityId(), "|", in.GetCurrency(), "|", amt,
		"|", in.GetReference().GetKind(), "/", in.GetReference().GetId(), "/", in.GetReference().GetRevision())))
	return hex.EncodeToString(sum[:])
}

// kind is erp/credit's participation in protocol 04 (be-protocol chapter 16): the branch's state is the SDK's
// (reserve), what it takes is the order exposure of one limit row.
func kind(m *core.Base) reserve.Kind {
	return reserve.Kind{Name: Kind, Period: core.HoldTTL,
		Lock: func(ctx context.Context, tx db.Tx, id string) error { // the limit row before the branch
			var limitID *string
			err := tx.QueryRow(ctx, `SELECT limit_id::text FROM branch WHERE id = $1`, id).Scan(&limitID)
			if errors.Is(err, pgx.ErrNoRows) || err == nil && limitID == nil {
				return nil // a tombstone, or a branch refused before it had a limit
			}
			if err != nil {
				return err
			}
			return limits.Lock(ctx, tx, *limitID)
		},
		GiveBack: func(ctx context.Context, tx db.Tx, b reserve.Branch) error {
			var limitID *string
			var amount, consumed string
			err := tx.QueryRow(ctx, `SELECT limit_id::text, amount::text, consumed::text FROM branch WHERE id = $1`, b.ID).
				Scan(&limitID, &amount, &consumed)
			if err != nil || limitID == nil {
				return err
			}
			return limits.GiveBackOrder(m, ctx, tx, *limitID, amount, consumed)
		},
		Changed: func(ctx context.Context, tx db.Tx, b reserve.Branch) error {
			out, err := core.GetBranch(ctx, tx, b.ID, false)
			if err != nil {
				return err
			}
			return core.Emit(m, ctx, tx, core.BranchChanged, out.ID, out.Version, out)
		}}
}

// Kind names this participation's branches in the SDK's table.
const Kind = "erp.credit.order"

// answered is the branch after a command, read in the command's transaction; Absent answers ABSENT.
func answered(ctx context.Context, tx db.Tx, a reserve.Answer) (*creditv3.Branch, error) {
	if a.Branch.State == reserve.Absent {
		return &creditv3.Branch{BranchId: a.Branch.ID, State: creditv3.BranchState_BRANCH_STATE_ABSENT}, nil
	}
	b, err := core.GetBranch(ctx, tx, a.Branch.ID, false)
	if err != nil {
		return nil, err
	}
	return wire(b), nil
}

// Hold takes the amount from the limit of (partner, legal entity, currency) under the limit row's lock.
func Hold(m *core.Base, ctx context.Context, in *creditv3.HoldRequest) (*creditv3.HoldResponse, error) {
	if err := branchID(in.GetBranchId()); err != nil {
		return nil, err
	}
	switch {
	case !core.UUIDForm.MatchString(in.GetPartnerId()):
		return nil, core.Invalid("partner_id", "partner_id is a partner's ID")
	case !core.UUIDForm.MatchString(in.GetLegalEntityId()):
		return nil, core.Invalid("legal_entity_id", "legal_entity_id is a legal entity's ID")
	case !core.CurrencyForm.MatchString(in.GetCurrency()):
		return nil, core.Invalid("currency", "currency is an ISO 4217 code")
	}
	amt, err := core.Amount("amount", in.GetAmount(), 4, false)
	if err != nil {
		return nil, err
	}
	id := in.GetBranchId()
	var out *creditv3.Branch
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		a, err := reserve.Hold(ctx, tx, kind(m), id, holdContent(in, amt), func(ctx context.Context, tx db.Tx) (bool, string, error) {
			return take(m, ctx, tx, in, amt)
		})
		if err != nil {
			return err
		}
		if a.Conflict {
			return core.ErrContent.New("branch " + id + " was held with other content")
		}
		out, err = answered(ctx, tx, a)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &creditv3.HoldResponse{Branch: out}, nil
}

// take is a new branch's hold: the limit row (made for a customer outside credit control under an unlimited policy)
// locked, the amount taken when it fits an active limit, the branch's row written either way; an over-limit refusal
// is a fact for the credit manager.
func take(m *core.Base, ctx context.Context, tx db.Tx, in *creditv3.HoldRequest, amt string) (bool, string, error) {
	var limitID string
	err := tx.QueryRow(ctx, `SELECT id::text FROM credit_limit WHERE partner_id = $1 AND legal_entity_id = $2
		AND currency = $3 FOR UPDATE`, in.GetPartnerId(), in.GetLegalEntityId(), in.GetCurrency()).Scan(&limitID)
	reason, avail := "", (*string)(nil)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return false, "", err
		}
		p, err := limits.PolicyOf(ctx, tx, in.GetLegalEntityId(), false)
		if err != nil {
			return false, "", err
		}
		if p.WithoutLimit == "block" {
			reason = "no_limit"
		} else {
			if _, err := tx.Exec(ctx, `INSERT INTO credit_limit (id, partner_id, legal_entity_id, currency)
				VALUES ($1, $2, $3, $4) ON CONFLICT (partner_id, legal_entity_id, currency) DO NOTHING`,
				ids.New().String(), in.GetPartnerId(), in.GetLegalEntityId(), in.GetCurrency()); err != nil {
				return false, "", err
			}
			if err := tx.QueryRow(ctx, `SELECT id::text FROM credit_limit WHERE partner_id = $1 AND legal_entity_id = $2
				AND currency = $3 FOR UPDATE`, in.GetPartnerId(), in.GetLegalEntityId(), in.GetCurrency()).Scan(&limitID); err != nil {
				return false, "", err
			}
		}
	}
	if limitID != "" {
		l, err := core.GetLimit(ctx, tx, limitID, false)
		if err != nil {
			return false, "", err
		}
		avail = l.Available
		switch {
		case l.State != "active":
			reason = "limit_suspended"
		case l.Available != nil:
			var ok bool
			if err := tx.QueryRow(ctx, `SELECT $1::numeric >= $2::numeric`, *l.Available, amt).Scan(&ok); err != nil {
				return false, "", err
			}
			if !ok {
				reason = "over_limit"
			}
		}
		if reason == "" {
			if err := limits.TakeOrder(m, ctx, tx, limitID, amt); err != nil {
				return false, "", err
			}
		}
	}
	var limitCol *string
	if limitID != "" {
		limitCol = &limitID
	}
	ref := in.GetReference()
	if _, err := tx.Exec(ctx, `INSERT INTO branch (id, limit_id, partner_id, legal_entity_id, currency, amount, ref_kind,
		ref_id, ref_revision, available) VALUES ($1, $2, $3, $4, $5, $6::numeric, $7, $8, $9, $10::numeric)`,
		in.GetBranchId(), limitCol, in.GetPartnerId(), in.GetLegalEntityId(), in.GetCurrency(), amt, ref.GetKind(),
		ref.GetId(), ref.GetRevision(), avail); err != nil {
		return false, "", err
	}
	if reason == "over_limit" {
		out, err := core.GetBranch(ctx, tx, in.GetBranchId(), false)
		if err != nil {
			return false, "", err
		}
		if _, err := m.RT.Events.Emit(ctx, tx, core.Stream, events.Event{Type: core.LimitExceeded, RecordID: out.ID, Version: 1,
			Projection: events.Full, LegalEntity: in.GetLegalEntityId(), Data: map[string]any{
				"fact_id": out.ID, "partner_id": in.GetPartnerId(), "legal_entity_id": in.GetLegalEntityId(),
				"currency": in.GetCurrency(), "requested": amt, "available": str(avail), "reference": out.Reference,
				"occurred_at": out.UpdatedAt}}); err != nil {
			return false, "", err
		}
	}
	return reason == "", reason, nil
}

// onBranch runs a command on an existing branch in a transaction and answers it (ABSENT for none stored).
func onBranch(m *core.Base, ctx context.Context, id string, command func(ctx context.Context, tx db.Tx) (reserve.Answer, error)) (*creditv3.Branch, error) {
	if err := branchID(id); err != nil {
		return nil, err
	}
	var out *creditv3.Branch
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		a, err := command(ctx, tx)
		if err != nil {
			return err
		}
		out, err = answered(ctx, tx, a)
		return err
	})
	return out, err
}

// Confirm turns a held branch into a committed one; a held branch past its period expires here instead.
func Confirm(m *core.Base, ctx context.Context, in *creditv3.ConfirmRequest) (*creditv3.ConfirmResponse, error) {
	b, err := onBranch(m, ctx, in.GetBranchId(), func(ctx context.Context, tx db.Tx) (reserve.Answer, error) {
		return reserve.Confirm(ctx, tx, kind(m), in.GetBranchId())
	})
	if err != nil {
		return nil, err
	}
	return &creditv3.ConfirmResponse{Branch: b}, nil
}

// Cancel releases what the branch has not consumed; an unknown branch becomes a tombstone that refuses a late hold.
func Cancel(m *core.Base, ctx context.Context, in *creditv3.CancelRequest) (*creditv3.CancelResponse, error) {
	b, err := onBranch(m, ctx, in.GetBranchId(), func(ctx context.Context, tx db.Tx) (reserve.Answer, error) {
		return reserve.Cancel(ctx, tx, kind(m), in.GetBranchId())
	})
	if err != nil {
		return nil, err
	}
	return &creditv3.CancelResponse{Branch: b}, nil
}

// Query answers the branch as it is; it leaves no tombstone.
func Query(m *core.Base, ctx context.Context, in *creditv3.QueryRequest) (*creditv3.QueryResponse, error) {
	b, err := onBranch(m, ctx, in.GetBranchId(), func(ctx context.Context, tx db.Tx) (reserve.Answer, error) {
		return reserve.Query(ctx, tx, kind(m), in.GetBranchId())
	})
	if err != nil {
		return nil, err
	}
	return &creditv3.QueryResponse{Branch: b}, nil
}

// Consume takes an amount off a committed branch, idempotent by consumption_id (the receivable side comes with
// protocol 05's conversion, later).
func Consume(m *core.Base, ctx context.Context, in *creditv3.ConsumeRequest) (*creditv3.ConsumeResponse, error) {
	if in.GetConsumptionId() == "" || len(in.GetConsumptionId()) > 128 {
		return nil, core.Invalid("consumption_id", "consumption_id is required")
	}
	amt, err := core.Amount("amount", in.GetAmount(), 4, false)
	if err != nil {
		return nil, err
	}
	b, err := onBranch(m, ctx, in.GetBranchId(), func(ctx context.Context, tx db.Tx) (reserve.Answer, error) {
		a, err := reserve.Consume(ctx, tx, kind(m), in.GetBranchId(), in.GetConsumptionId(), "amount:"+amt,
			func(ctx context.Context, tx db.Tx, b reserve.Branch) (bool, bool, error) {
				var fits bool
				var limitID string
				if err := tx.QueryRow(ctx, `SELECT consumed + $2::numeric <= amount, limit_id::text FROM branch WHERE id = $1`,
					b.ID, amt).Scan(&fits, &limitID); err != nil || !fits {
					return false, !fits, err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO consumption (id, branch_id, amount) VALUES ($1, $2, $3::numeric)`,
					in.GetConsumptionId(), b.ID, amt); err != nil {
					return false, false, err
				}
				var left bool
				if err := tx.QueryRow(ctx, `UPDATE branch SET consumed = consumed + $2::numeric WHERE id = $1
					RETURNING consumed < amount`, b.ID, amt).Scan(&left); err != nil {
					return false, false, err
				}
				return left, false, limits.ConsumeOrder(m, ctx, tx, limitID, amt)
			})
		switch {
		case err != nil:
			return a, err
		case a.Conflict:
			return a, core.ErrContent.New("consumption " + in.GetConsumptionId() + " was used with other content")
		case a.Refused == reserve.NotCommitted:
			return a, core.ErrState.New("only a committed branch is consumed; it is " + string(a.Branch.State))
		case a.Refused == reserve.BeyondRemaining:
			return a, core.ErrState.New("more than the branch has left")
		}
		return a, nil // NoBranch answers ABSENT, as a query does
	})
	if err != nil {
		return nil, err
	}
	return &creditv3.ConsumeResponse{Branch: b}, nil
}

// Adjust applies an order revision (protocol 04): not in this version.
func Adjust(_ *core.Base, _ context.Context, _ *creditv3.AdjustRequest) (*creditv3.AdjustResponse, error) {
	return nil, errs.New(errs.ReasonCapabilityUnsupported, "branch adjustment (order revisions) is not in this version of erp/credit")
}

// ExpireHolds releases held branches past their period (the SDK's expiry, a batch a run).
func ExpireHolds(m *core.Base, ctx context.Context, _ *work.Run) error {
	return reserve.ExpireDue(ctx, m.RT.DB, kind(m))
}

// Available answers the credit of (partner, legal entity, currency) for a coordinator to show.
func Available(m *core.Base, ctx context.Context, in *creditv3.AvailableRequest) (*creditv3.AvailableResponse, error) {
	if !core.UUIDForm.MatchString(in.GetPartnerId()) || !core.UUIDForm.MatchString(in.GetLegalEntityId()) || !core.CurrencyForm.MatchString(in.GetCurrency()) {
		return nil, core.Invalid("partner_id", "partner_id, legal_entity_id and currency are required")
	}
	out := &creditv3.AvailableResponse{}
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		p, err := limits.PolicyOf(ctx, tx, in.GetLegalEntityId(), false)
		if err != nil {
			return err
		}
		out.Policy = p.WithoutLimit
		l, err := core.One(tx.QueryRow(ctx, `SELECT `+core.LimitCols+` FROM credit_limit c WHERE c.partner_id = $1 AND
			c.legal_entity_id = $2 AND c.currency = $3`, in.GetPartnerId(), in.GetLegalEntityId(), in.GetCurrency()), core.ScanLimit)
		if errors.Is(err, core.ErrNone) {
			return nil
		}
		if err != nil {
			return err
		}
		out.HasLimit, out.Limit, out.OrderExposure, out.ReceivableExposure = true, str(l.Limit), l.OrderExposure, l.ReceivableExposure
		out.Available, out.State = str(l.Available), l.State
		return nil
	})
	return out, err
}

// GetBranchHTTP shows a branch to a user who may read limits of its legal entity; a tombstone is not shown.
func GetBranchHTTP(m *core.Base, c *httpx.Context) error {
	id := c.Param("id")
	if !core.UUIDForm.MatchString(id) {
		return core.NotFound("branch")
	}
	var b core.Branch
	err := m.RT.DB.Tx(c.Ctx(), func(ctx context.Context, tx db.Tx) error {
		var err error
		b, err = core.GetBranch(ctx, tx, id, false)
		return err
	})
	if errors.Is(err, core.ErrNone) || err == nil && b.LegalEntityID == nil {
		return core.NotFound("branch")
	}
	if err != nil {
		return err
	}
	if _, _, err := core.Check(c, core.OpLimitRead, *b.LegalEntityID); err != nil {
		return err
	}
	return c.JSON(200, b)
}
