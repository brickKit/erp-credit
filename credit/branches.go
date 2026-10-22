package credit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/errs"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/ids"
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

func wire(b Branch) *creditv3.Branch {
	return &creditv3.Branch{BranchId: b.ID, State: branchStates[b.State], Reason: str(b.Reason), PartnerId: str(b.PartnerID),
		LegalEntityId: str(b.LegalEntityID), Currency: str(b.Currency), Amount: str(b.Amount), Consumed: str(b.Consumed),
		Available: str(b.available), HoldExpiresAt: str(b.HoldExpiresAt), Version: b.Version}
}

func branchID(id string) error {
	if !uuidForm.MatchString(id) {
		return invalid("branch_id", "branch_id is the coordinator's UUID for the branch")
	}
	return nil
}

func holdContent(in *creditv3.HoldRequest, amt string) string {
	sum := sha256.Sum256([]byte(fmt.Sprint(in.GetPartnerId(), "|", in.GetLegalEntityId(), "|", in.GetCurrency(), "|", amt,
		"|", in.GetReference().GetKind(), "/", in.GetReference().GetId(), "/", in.GetReference().GetRevision())))
	return hex.EncodeToString(sum[:])
}

// Hold takes the amount from the limit of (partner, legal entity, currency) under the limit row's lock.
func (m *Module) Hold(ctx context.Context, in *creditv3.HoldRequest) (*creditv3.HoldResponse, error) {
	if err := branchID(in.GetBranchId()); err != nil {
		return nil, err
	}
	switch {
	case !uuidForm.MatchString(in.GetPartnerId()):
		return nil, invalid("partner_id", "partner_id is a partner's ID")
	case !uuidForm.MatchString(in.GetLegalEntityId()):
		return nil, invalid("legal_entity_id", "legal_entity_id is a legal entity's ID")
	case !currencyForm.MatchString(in.GetCurrency()):
		return nil, invalid("currency", "currency is an ISO 4217 code")
	}
	amt, err := amount("amount", in.GetAmount(), 4, false)
	if err != nil {
		return nil, err
	}
	digest := holdContent(in, amt)
	var out Branch
	err = m.rt.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO branch (id, state, content_digest) VALUES ($1, 'held', $2)
			ON CONFLICT (id) DO NOTHING`, in.GetBranchId(), digest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 { // seen before: a tombstone, the same hold again, or a conflict
			b, err := getBranch(ctx, tx, in.GetBranchId(), true)
			if err != nil {
				return err
			}
			if b.State != "cancelled" && str(b.digest) != digest {
				return errContent.New("branch " + b.ID + " was held with other content")
			}
			out = b
			return nil
		}
		// the limit row, made for a customer outside credit control under an unlimited policy
		var limitID string
		err = tx.QueryRow(ctx, `SELECT id::text FROM credit_limit WHERE partner_id = $1 AND legal_entity_id = $2
			AND currency = $3 FOR UPDATE`, in.GetPartnerId(), in.GetLegalEntityId(), in.GetCurrency()).Scan(&limitID)
		state, reason, avail := "held", "", (*string)(nil)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			p, err := policyOf(ctx, tx, in.GetLegalEntityId(), false)
			if err != nil {
				return err
			}
			if p.WithoutLimit == "block" {
				state, reason = "rejected", "no_limit"
			} else {
				if _, err := tx.Exec(ctx, `INSERT INTO credit_limit (id, partner_id, legal_entity_id, currency)
					VALUES ($1, $2, $3, $4) ON CONFLICT (partner_id, legal_entity_id, currency) DO NOTHING`,
					ids.New().String(), in.GetPartnerId(), in.GetLegalEntityId(), in.GetCurrency()); err != nil {
					return err
				}
				if err := tx.QueryRow(ctx, `SELECT id::text FROM credit_limit WHERE partner_id = $1 AND legal_entity_id = $2
					AND currency = $3 FOR UPDATE`, in.GetPartnerId(), in.GetLegalEntityId(), in.GetCurrency()).Scan(&limitID); err != nil {
					return err
				}
			}
		}
		if limitID != "" {
			l, err := getLimit(ctx, tx, limitID, false)
			if err != nil {
				return err
			}
			avail = l.Available
			switch {
			case l.State != "active":
				state, reason = "rejected", "limit_suspended"
			case l.Available != nil:
				var ok bool
				if err := tx.QueryRow(ctx, `SELECT $1::numeric >= $2::numeric`, *l.Available, amt).Scan(&ok); err != nil {
					return err
				}
				if !ok {
					state, reason = "rejected", "over_limit"
				}
			}
			if state == "held" {
				if _, err := tx.Exec(ctx, `UPDATE credit_limit SET order_exposure = order_exposure + $2::numeric,
					version = version + 1, updated_at = now() WHERE id = $1`, limitID, amt); err != nil {
					return err
				}
				if _, err := m.emitLimit(ctx, tx, limitID); err != nil {
					return err
				}
			}
		}
		var limitCol, reasonCol *string
		if limitID != "" {
			limitCol = &limitID
		}
		if reason != "" {
			reasonCol = &reason
		}
		ref := in.GetReference()
		if _, err := tx.Exec(ctx, `UPDATE branch SET limit_id = $2, partner_id = $3, legal_entity_id = $4, currency = $5,
			amount = $6::numeric, ref_kind = $7, ref_id = $8, ref_revision = $9, state = $10, reason = $11,
			available = $12::numeric, hold_expires_at = CASE WHEN $10 = 'held' THEN now() + $13::interval END WHERE id = $1`,
			in.GetBranchId(), limitCol, in.GetPartnerId(), in.GetLegalEntityId(), in.GetCurrency(), amt, ref.GetKind(),
			ref.GetId(), ref.GetRevision(), state, reasonCol, avail,
			fmt.Sprintf("%d seconds", int(HoldTTL.Seconds()))); err != nil {
			return err
		}
		if out, err = getBranch(ctx, tx, in.GetBranchId(), false); err != nil {
			return err
		}
		if reason == "over_limit" {
			if _, err := m.rt.Events.Emit(ctx, tx, Stream, events.Event{Type: LimitExceeded, RecordID: out.ID, Version: 1,
				Projection: events.Full, LegalEntity: in.GetLegalEntityId(), Data: map[string]any{
					"fact_id": out.ID, "partner_id": in.GetPartnerId(), "legal_entity_id": in.GetLegalEntityId(),
					"currency": in.GetCurrency(), "requested": amt, "available": str(avail), "reference": out.Reference,
					"occurred_at": out.UpdatedAt}}); err != nil {
				return err
			}
		}
		return m.emit(ctx, tx, BranchChanged, out.ID, out.Version, out)
	})
	if err != nil {
		return nil, err
	}
	return &creditv3.HoldResponse{Branch: wire(out)}, nil
}

// release gives back what a held or committed branch has not consumed and moves it to state.
func (m *Module) release(ctx context.Context, tx db.Tx, b Branch, state string) (Branch, error) {
	if b.limitID != nil {
		if _, err := getLimit(ctx, tx, *b.limitID, true); err != nil {
			return b, err
		}
		if _, err := tx.Exec(ctx, `UPDATE credit_limit SET order_exposure = order_exposure - ($2::numeric - $3::numeric),
			version = version + 1, updated_at = now() WHERE id = $1`, *b.limitID, str(b.Amount), str(b.Consumed)); err != nil {
			return b, err
		}
		if _, err := m.emitLimit(ctx, tx, *b.limitID); err != nil {
			return b, err
		}
	}
	return m.setState(ctx, tx, b.ID, state)
}

func (m *Module) setState(ctx context.Context, tx db.Tx, id, state string) (Branch, error) {
	if _, err := tx.Exec(ctx, `UPDATE branch SET state = $2, hold_expires_at = CASE WHEN $2 = 'held' THEN hold_expires_at END,
		version = version + 1, updated_at = now() WHERE id = $1`, id, state); err != nil {
		return Branch{}, err
	}
	b, err := getBranch(ctx, tx, id, false)
	if err != nil {
		return b, err
	}
	return b, m.emit(ctx, tx, BranchChanged, b.ID, b.Version, b)
}

// lockBranch locks a branch: its limit row first, then the branch, the order every command uses.
func lockBranch(ctx context.Context, tx db.Tx, id string) (Branch, error) {
	b, err := getBranch(ctx, tx, id, false)
	if err != nil {
		return b, err
	}
	if b.limitID != nil {
		if _, err := getLimit(ctx, tx, *b.limitID, true); err != nil {
			return b, err
		}
	}
	return getBranch(ctx, tx, id, true)
}

// onBranch locks an existing branch and applies fn; an unknown branch answers ABSENT.
func (m *Module) onBranch(ctx context.Context, id string, fn func(ctx context.Context, tx db.Tx, b Branch) (Branch, error)) (*creditv3.Branch, error) {
	if err := branchID(id); err != nil {
		return nil, err
	}
	var out Branch
	err := m.rt.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		b, err := lockBranch(ctx, tx, id)
		if err != nil {
			return err
		}
		out, err = fn(ctx, tx, b)
		return err
	})
	if errors.Is(err, errNone) {
		return &creditv3.Branch{BranchId: id, State: creditv3.BranchState_BRANCH_STATE_ABSENT}, nil
	}
	if err != nil {
		return nil, err
	}
	return wire(out), nil
}

// Confirm turns a held branch into a committed one; a held branch past its period expires here instead.
func (m *Module) Confirm(ctx context.Context, in *creditv3.ConfirmRequest) (*creditv3.ConfirmResponse, error) {
	b, err := m.onBranch(ctx, in.GetBranchId(), func(ctx context.Context, tx db.Tx, b Branch) (Branch, error) {
		if b.State != "held" {
			return b, nil
		}
		var past bool
		if err := tx.QueryRow(ctx, `SELECT hold_expires_at <= now() FROM branch WHERE id = $1`, b.ID).Scan(&past); err != nil {
			return b, err
		}
		if past {
			return m.release(ctx, tx, b, "expired")
		}
		return m.setState(ctx, tx, b.ID, "committed")
	})
	if err != nil {
		return nil, err
	}
	return &creditv3.ConfirmResponse{Branch: b}, nil
}

// Cancel releases what the branch has not consumed; an unknown branch becomes a tombstone that refuses a late hold.
func (m *Module) Cancel(ctx context.Context, in *creditv3.CancelRequest) (*creditv3.CancelResponse, error) {
	if err := branchID(in.GetBranchId()); err != nil {
		return nil, err
	}
	var out Branch
	err := m.rt.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO branch (id, state) VALUES ($1, 'cancelled') ON CONFLICT (id) DO NOTHING`,
			in.GetBranchId())
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			if out, err = getBranch(ctx, tx, in.GetBranchId(), false); err != nil {
				return err
			}
			return m.emit(ctx, tx, BranchChanged, out.ID, out.Version, out)
		}
		b, err := lockBranch(ctx, tx, in.GetBranchId())
		if err != nil {
			return err
		}
		out = b
		if b.State == "held" || b.State == "committed" {
			out, err = m.release(ctx, tx, b, "released")
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return &creditv3.CancelResponse{Branch: wire(out)}, nil
}

// Query answers the branch as it is; it leaves no tombstone.
func (m *Module) Query(ctx context.Context, in *creditv3.QueryRequest) (*creditv3.QueryResponse, error) {
	b, err := m.onBranch(ctx, in.GetBranchId(), func(_ context.Context, _ db.Tx, b Branch) (Branch, error) { return b, nil })
	if err != nil {
		return nil, err
	}
	return &creditv3.QueryResponse{Branch: b}, nil
}

// Consume takes an amount off a committed branch, idempotent by consumption_id (the receivable side comes with
// protocol 05's conversion, later).
func (m *Module) Consume(ctx context.Context, in *creditv3.ConsumeRequest) (*creditv3.ConsumeResponse, error) {
	if in.GetConsumptionId() == "" || len(in.GetConsumptionId()) > 128 {
		return nil, invalid("consumption_id", "consumption_id is required")
	}
	amt, err := amount("amount", in.GetAmount(), 4, false)
	if err != nil {
		return nil, err
	}
	b, err := m.onBranch(ctx, in.GetBranchId(), func(ctx context.Context, tx db.Tx, b Branch) (Branch, error) {
		tag, err := tx.Exec(ctx, `INSERT INTO consumption (id, branch_id, amount) VALUES ($1, $2, $3::numeric)
			ON CONFLICT (id) DO NOTHING`, in.GetConsumptionId(), b.ID, amt)
		if err != nil {
			return b, err
		}
		if tag.RowsAffected() == 0 {
			var sameBranch, sameAmount bool
			if err := tx.QueryRow(ctx, `SELECT branch_id = $2, amount = $3::numeric FROM consumption WHERE id = $1`,
				in.GetConsumptionId(), b.ID, amt).Scan(&sameBranch, &sameAmount); err != nil {
				return b, err
			}
			if !sameBranch || !sameAmount {
				return b, errContent.New("consumption " + in.GetConsumptionId() + " was used with other content")
			}
			return b, nil
		}
		if b.State != "committed" {
			return b, errState.New("only a committed branch is consumed; it is " + b.State)
		}
		var fits bool
		if err := tx.QueryRow(ctx, `SELECT consumed + $2::numeric <= amount FROM branch WHERE id = $1`, b.ID, amt).
			Scan(&fits); err != nil {
			return b, err
		}
		if !fits {
			return b, errState.New("more than the branch has left")
		}
		if _, err := tx.Exec(ctx, `UPDATE branch SET consumed = consumed + $2::numeric WHERE id = $1`, b.ID, amt); err != nil {
			return b, err
		}
		if _, err := tx.Exec(ctx, `UPDATE credit_limit SET order_exposure = order_exposure - $2::numeric, version = version + 1,
			updated_at = now() WHERE id = $1`, *b.limitID, amt); err != nil {
			return b, err
		}
		if _, err := m.emitLimit(ctx, tx, *b.limitID); err != nil {
			return b, err
		}
		var left bool
		if err := tx.QueryRow(ctx, `SELECT consumed < amount FROM branch WHERE id = $1`, b.ID).Scan(&left); err != nil {
			return b, err
		}
		state := "committed"
		if !left {
			state = "consumed"
		}
		return m.setState(ctx, tx, b.ID, state)
	})
	if err != nil {
		return nil, err
	}
	return &creditv3.ConsumeResponse{Branch: b}, nil
}

// Adjust applies an order revision (protocol 04): not in this version.
func (m *Module) Adjust(context.Context, *creditv3.AdjustRequest) (*creditv3.AdjustResponse, error) {
	return nil, errs.New(errs.ReasonCapabilityUnsupported, "branch adjustment (order revisions) is not in this version of erp/credit")
}

// expireHolds releases held branches past their period, a batch a run.
func (m *Module) expireHolds(ctx context.Context, _ *work.Run) error {
	for {
		var due []string
		err := m.rt.DB.BackgroundTx(ctx, func(ctx context.Context, tx db.Tx) error {
			due = due[:0]
			rows, err := tx.Query(ctx, `SELECT id::text FROM branch WHERE state = 'held' AND hold_expires_at <= now()
				ORDER BY hold_expires_at LIMIT 100`)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				due = append(due, id)
			}
			rows.Close()
			return nil
		})
		if err != nil {
			return err
		}
		for _, id := range due { // each in its own transaction, in the limit-then-branch lock order
			if err := m.rt.DB.BackgroundTx(ctx, func(ctx context.Context, tx db.Tx) error {
				b, err := lockBranch(ctx, tx, id)
				if err != nil || b.State != "held" {
					return err
				}
				var past bool
				if err := tx.QueryRow(ctx, `SELECT hold_expires_at <= now() FROM branch WHERE id = $1`, id).Scan(&past); err != nil || !past {
					return err
				}
				_, err = m.release(ctx, tx, b, "expired")
				return err
			}); err != nil {
				return err
			}
		}
		if len(due) < 100 {
			return nil
		}
	}
}

// Jobs declares the background work: the expiry of holds (protocol 01).
func (m *Module) Jobs(j *work.Jobs) error {
	j.Add(work.Job{Name: "expire-holds", Kind: work.Singleton, Effect: work.Local, Interval: 10 * time.Second,
		MaxAttempts: work.Unlimited, Handle: m.expireHolds})
	return nil
}

// Available answers the credit of (partner, legal entity, currency) for a coordinator to show.
func (m *Module) Available(ctx context.Context, in *creditv3.AvailableRequest) (*creditv3.AvailableResponse, error) {
	if !uuidForm.MatchString(in.GetPartnerId()) || !uuidForm.MatchString(in.GetLegalEntityId()) || !currencyForm.MatchString(in.GetCurrency()) {
		return nil, invalid("partner_id", "partner_id, legal_entity_id and currency are required")
	}
	out := &creditv3.AvailableResponse{}
	err := m.rt.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		p, err := policyOf(ctx, tx, in.GetLegalEntityId(), false)
		if err != nil {
			return err
		}
		out.Policy = p.WithoutLimit
		l, err := one(tx.QueryRow(ctx, `SELECT `+limitCols+` FROM credit_limit c WHERE c.partner_id = $1 AND
			c.legal_entity_id = $2 AND c.currency = $3`, in.GetPartnerId(), in.GetLegalEntityId(), in.GetCurrency()), scanLimit)
		if errors.Is(err, errNone) {
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

// getBranchHTTP shows a branch to a user who may read limits of its legal entity; a tombstone is not shown.
func (m *Module) getBranchHTTP(c *httpx.Context) error {
	id := c.Param("id")
	if !uuidForm.MatchString(id) {
		return notFound("branch")
	}
	var b Branch
	err := m.rt.DB.Tx(c.Ctx(), func(ctx context.Context, tx db.Tx) error {
		var err error
		b, err = getBranch(ctx, tx, id, false)
		return err
	})
	if errors.Is(err, errNone) || err == nil && b.LegalEntityID == nil {
		return notFound("branch")
	}
	if err != nil {
		return err
	}
	if _, _, err := check(c, OpLimitRead, *b.LegalEntityID); err != nil {
		return err
	}
	return c.JSON(200, b)
}
