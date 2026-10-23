package credit

import (
	"context"
	"errors"
	"time"

	"github.com/brickKit/be-sdk-go/v3/canon"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/jackc/pgx/v5"
)

// Policy is a legal entity's policy for customers without a limit (erp.credit_policy.changed.v1).
type Policy struct {
	ID            string `json:"id"`
	LegalEntityID string `json:"legal_entity_id"`
	WithoutLimit  string `json:"without_limit"`
	Version       int64  `json:"version"`
	UpdatedAt     string `json:"updated_at"`
}

// Limit is a customer's credit in one legal entity and currency (erp.credit_limit.changed.v1). Limit and Available
// are nil when the customer is not under credit control.
type Limit struct {
	ID                 string  `json:"id"`
	PartnerID          string  `json:"partner_id"`
	LegalEntityID      string  `json:"legal_entity_id"`
	Currency           string  `json:"currency"`
	Limit              *string `json:"limit"`
	State              string  `json:"state"`
	OrderExposure      string  `json:"order_exposure"`
	ReceivableExposure string  `json:"receivable_exposure"`
	Used               string  `json:"used"`
	Available          *string `json:"available"`
	ReceivablesTracked bool    `json:"receivables_tracked"`
	Version            int64   `json:"version"`
	UpdatedAt          string  `json:"updated_at"`
}

// Reference names a coordinator's document on a branch.
type Reference struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Revision *int64 `json:"revision,omitempty"`
}

// Branch is a credit reservation branch (erp.credit_branch.changed.v1); the limit and the digest stay inside.
type Branch struct {
	ID            string     `json:"id"`
	PartnerID     *string    `json:"partner_id"`
	LegalEntityID *string    `json:"legal_entity_id"`
	Currency      *string    `json:"currency"`
	Amount        *string    `json:"amount"`
	Consumed      *string    `json:"consumed"`
	Reference     *Reference `json:"reference"`
	State         string     `json:"state"`
	Reason        *string    `json:"reason"`
	HoldExpiresAt *string    `json:"hold_expires_at"`
	Version       int64      `json:"version"`
	UpdatedAt     string     `json:"updated_at"`

	limitID   *string
	available *string
	digest    *string
}

const (
	policyCols = `p.legal_entity_id::text, p.without_limit, p.version, p.updated_at`
	limitCols  = `c.id::text, c.partner_id::text, c.legal_entity_id::text, c.currency, trim_scale(c.amount)::text, c.state,
		trim_scale(c.order_exposure)::text, trim_scale(c.receivable_exposure)::text,
		trim_scale(c.order_exposure + c.receivable_exposure)::text,
		trim_scale(c.amount - c.order_exposure - c.receivable_exposure)::text, c.version, c.updated_at`
	branchCols = `b.id::text, b.partner_id::text, b.legal_entity_id::text, b.currency, trim_scale(b.amount)::text,
		CASE WHEN b.amount IS NULL THEN NULL ELSE trim_scale(b.consumed)::text END, b.ref_kind, b.ref_id, b.ref_revision,
		b.state, b.reason, b.hold_expires_at, b.version, b.updated_at, b.limit_id::text, trim_scale(b.available)::text,
		b.content_digest`
)

func scanPolicy(row pgx.Row) (Policy, error) {
	var p Policy
	var at time.Time
	err := row.Scan(&p.LegalEntityID, &p.WithoutLimit, &p.Version, &at)
	p.ID, p.UpdatedAt = p.LegalEntityID, canon.Time(at)
	return p, err
}

// receivablesTracked: this version does not consume erp/billing's billed fact, so receivable_exposure stays 0 and
// "used" is the order exposure only (docs/design.md, implementation status); a limit says so.
const receivablesTracked = false

func scanLimit(row pgx.Row) (Limit, error) {
	var l Limit
	var at time.Time
	err := row.Scan(&l.ID, &l.PartnerID, &l.LegalEntityID, &l.Currency, &l.Limit, &l.State, &l.OrderExposure,
		&l.ReceivableExposure, &l.Used, &l.Available, &l.Version, &at)
	l.UpdatedAt, l.ReceivablesTracked = canon.Time(at), receivablesTracked
	return l, err
}

func scanBranch(row pgx.Row) (Branch, error) {
	var b Branch
	var refKind, refID *string
	var refRev *int64
	var expires *time.Time
	var at time.Time
	err := row.Scan(&b.ID, &b.PartnerID, &b.LegalEntityID, &b.Currency, &b.Amount, &b.Consumed, &refKind, &refID, &refRev,
		&b.State, &b.Reason, &expires, &b.Version, &at, &b.limitID, &b.available, &b.digest)
	if err != nil {
		return b, err
	}
	if refKind != nil {
		b.Reference = &Reference{Kind: *refKind, ID: *refID, Revision: refRev}
	}
	if expires != nil && b.State == "held" {
		s := canon.Time(*expires)
		b.HoldExpiresAt = &s
	}
	b.UpdatedAt = canon.Time(at)
	return b, nil
}

// errNone is a lookup that found no row.
var errNone = errors.New("no such record")

func one[T any](row pgx.Row, scan func(pgx.Row) (T, error)) (T, error) {
	v, err := scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, errNone
	}
	return v, err
}

func getLimit(ctx context.Context, tx db.Tx, id string, lock bool) (Limit, error) {
	q := `SELECT ` + limitCols + ` FROM credit_limit c WHERE c.id = $1::uuid`
	if lock {
		q += ` FOR UPDATE`
	}
	return one(tx.QueryRow(ctx, q, id), scanLimit)
}

func getBranch(ctx context.Context, tx db.Tx, id string, lock bool) (Branch, error) {
	q := `SELECT ` + branchCols + ` FROM branch b WHERE b.id = $1::uuid`
	if lock {
		q += ` FOR UPDATE`
	}
	return one(tx.QueryRow(ctx, q, id), scanBranch)
}

// emit writes a full-state event into the outbox, in the transaction that changed the record (protocol 02, S2).
func (m *Module) emit(ctx context.Context, tx db.Tx, typ, id string, version int64, data any) error {
	_, err := m.rt.Events.Emit(ctx, tx, Stream, events.Event{Type: typ, RecordID: id, Version: version,
		Projection: events.Full, Data: data})
	return err
}

// emitLimit publishes a limit's state after its row changed.
func (m *Module) emitLimit(ctx context.Context, tx db.Tx, id string) (Limit, error) {
	l, err := getLimit(ctx, tx, id, false)
	if err != nil {
		return l, err
	}
	return l, m.emit(ctx, tx, LimitChanged, l.ID, l.Version, l)
}
