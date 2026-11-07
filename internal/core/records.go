package core

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

	LimitID   *string `json:"-"`
	Available *string `json:"-"`
	Digest    *string `json:"-"`
}

const (
	PolicyCols = `p.legal_entity_id::text, p.without_limit, p.version, p.updated_at`
	LimitCols  = `c.id::text, c.partner_id::text, c.legal_entity_id::text, c.currency, trim_scale(c.amount)::text, c.state,
		trim_scale(c.order_exposure)::text, trim_scale(c.receivable_exposure)::text,
		trim_scale(c.order_exposure + c.receivable_exposure)::text,
		trim_scale(c.amount - c.order_exposure - c.receivable_exposure)::text, c.version, c.updated_at`
	// a branch is the SDK's participant record (be_reserve_branch: state, reason, digest, hold period, version, times)
	// with what it takes here (branch; none on a tombstone)
	branchCols = `r.id::text, b.partner_id::text, b.legal_entity_id::text, b.currency, trim_scale(b.amount)::text,
		CASE WHEN b.amount IS NULL THEN NULL ELSE trim_scale(b.consumed)::text END, b.ref_kind, b.ref_id, b.ref_revision,
		r.state, r.reason, r.hold_expires_at, r.version, r.updated_at, b.limit_id::text, trim_scale(b.available)::text,
		r.content_digest`
)

func ScanPolicy(row pgx.Row) (Policy, error) {
	var p Policy
	var at time.Time
	err := row.Scan(&p.LegalEntityID, &p.WithoutLimit, &p.Version, &at)
	p.ID, p.UpdatedAt = p.LegalEntityID, canon.Time(at)
	return p, err
}

// receivablesTracked: this version does not consume erp/billing's billed fact, so receivable_exposure stays 0 and
// "used" is the order exposure only (docs/design.md, implementation status); a limit says so.
const receivablesTracked = false

func ScanLimit(row pgx.Row) (Limit, error) {
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
		&b.State, &b.Reason, &expires, &b.Version, &at, &b.LimitID, &b.Available, &b.Digest)
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
var ErrNone = errors.New("no such record")

func One[T any](row pgx.Row, scan func(pgx.Row) (T, error)) (T, error) {
	v, err := scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNone
	}
	return v, err
}

func GetLimit(ctx context.Context, tx db.Tx, id string, lock bool) (Limit, error) {
	q := `SELECT ` + LimitCols + ` FROM credit_limit c WHERE c.id = $1::uuid`
	if lock {
		q += ` FOR UPDATE`
	}
	return One(tx.QueryRow(ctx, q, id), ScanLimit)
}

func GetBranch(ctx context.Context, tx db.Tx, id string, lock bool) (Branch, error) {
	q := `SELECT ` + branchCols + ` FROM be_reserve_branch r LEFT JOIN branch b ON b.id = r.id WHERE r.id = $1::uuid`
	if lock {
		q += ` FOR UPDATE OF r`
	}
	return One(tx.QueryRow(ctx, q, id), scanBranch)
}

// Emit writes a full-state event into the outbox, in the transaction that changed the record (protocol 02, S2).
func Emit(m *Base, ctx context.Context, tx db.Tx, typ, id string, version int64, data any) error {
	_, err := m.RT.Events.Emit(ctx, tx, Stream, events.Event{Type: typ, RecordID: id, Version: version,
		Projection: events.Full, Data: data})
	return err
}

// EmitLimit publishes a limit's state after its row changed.
func EmitLimit(m *Base, ctx context.Context, tx db.Tx, id string) (Limit, error) {
	l, err := GetLimit(ctx, tx, id, false)
	if err != nil {
		return l, err
	}
	return l, Emit(m, ctx, tx, LimitChanged, l.ID, l.Version, l)
}
