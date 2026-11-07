package limits

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	core "github.com/brickKit/erp-credit/v3/internal/core"
	masterdata "github.com/brickKit/erp-credit/v3/internal/masterdata"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/errs"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/idem"
	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/list"
)

func PolicyOf(ctx context.Context, tx db.Tx, legalEntity string, lock bool) (core.Policy, error) {
	q := `SELECT ` + core.PolicyCols + ` FROM credit_policy p WHERE p.legal_entity_id = $1::uuid`
	if lock {
		q += ` FOR UPDATE`
	}
	p, err := core.One(tx.QueryRow(ctx, q, legalEntity), core.ScanPolicy)
	if errors.Is(err, core.ErrNone) { // no row is block (docs/design.md)
		return core.Policy{ID: legalEntity, LegalEntityID: legalEntity, WithoutLimit: "block", Version: 0}, nil
	}
	return p, err
}

func GetPolicy(m *core.Base, c *httpx.Context) error {
	le := c.Param("legal_entity_id")
	if !core.UUIDForm.MatchString(le) {
		return core.NotFound("policy")
	}
	ctx, _, err := core.Check(c, core.OpPolicyRead, le)
	if err != nil {
		return err
	}
	var p core.Policy
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		p, err = PolicyOf(ctx, tx, le, false)
		return err
	})
	if err != nil {
		return err
	}
	return c.JSON(200, p)
}

// SetPolicy sets a legal entity's policy for customers without a limit; the legal entity must be active now.
func SetPolicy(m *core.Base, c *httpx.Context) error {
	le := c.Param("legal_entity_id")
	if !core.UUIDForm.MatchString(le) {
		return core.NotFound("policy")
	}
	var in struct {
		WithoutLimit string `json:"without_limit"`
		Version      *int64 `json:"version"`
	}
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if in.WithoutLimit != "block" && in.WithoutLimit != "unlimited" {
		return core.Invalid("without_limit", "without_limit is block or unlimited")
	}
	ctx, actor, err := core.Check(c, core.OpPolicyManage, le)
	if err != nil {
		return err
	}
	if err := masterdata.LegalEntityActive(m, ctx, le); err != nil {
		return err
	}
	var out core.Policy
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO credit_policy (legal_entity_id, without_limit, version) VALUES ($1, 'block', 0)
			ON CONFLICT DO NOTHING`, le); err != nil {
			return err
		}
		cur, err := PolicyOf(ctx, tx, le, true)
		if err != nil {
			return err
		}
		if in.Version != nil && *in.Version != cur.Version {
			return core.ErrVersion.New(fmt.Sprintf("the policy is at version %d", cur.Version))
		}
		if cur.WithoutLimit == in.WithoutLimit && cur.Version > 0 {
			out = cur
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE credit_policy SET without_limit = $2, version = version + 1, updated_at = now()
			WHERE legal_entity_id = $1`, le, in.WithoutLimit); err != nil {
			return err
		}
		if out, err = PolicyOf(ctx, tx, le, false); err != nil {
			return err
		}
		if err := core.Emit(m, ctx, tx, core.PolicyChanged, out.ID, out.Version, out); err != nil {
			return err
		}
		return core.Change(m, ctx, tx, actor, core.OpPolicyManage, core.Policies, out.ID, out)
	})
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}

// CreateLimit sets a customer's limit, once per idempotency key; the partner must be an active customer of the legal
// entity and the currency active. limit null puts the customer outside credit control.
func CreateLimit(m *core.Base, c *httpx.Context) error {
	var in struct {
		PartnerID     string          `json:"partner_id"`
		LegalEntityID string          `json:"legal_entity_id"`
		Currency      string          `json:"currency"`
		Limit         json.RawMessage `json:"limit"`
	}
	req, err := idem.FromHTTP(c)
	if err != nil {
		return err
	}
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	switch {
	case !core.UUIDForm.MatchString(in.PartnerID):
		return core.Invalid("partner_id", "partner_id is a partner's ID")
	case !core.UUIDForm.MatchString(in.LegalEntityID):
		return core.Invalid("legal_entity_id", "legal_entity_id is a legal entity's ID")
	case !core.CurrencyForm.MatchString(in.Currency):
		return core.Invalid("currency", "currency is an ISO 4217 code")
	case len(in.Limit) == 0:
		return core.Invalid("limit", "limit is required: a decimal string, or null for no credit control")
	}
	ctx, actor, err := core.Check(c, core.OpLimitManage, in.LegalEntityID)
	if err != nil {
		return err
	}
	scale, err := masterdata.CurrencyScale(m, ctx, in.Currency)
	if err != nil {
		return err
	}
	var amt *string
	if string(in.Limit) != "null" {
		var s string
		if json.Unmarshal(in.Limit, &s) != nil {
			return core.Invalid("limit", "limit is a decimal string, or null")
		}
		v, err := core.Amount("limit", s, scale, true)
		if err != nil {
			return err
		}
		amt = &v
	}
	if err := masterdata.CustomerEligible(m, ctx, in.PartnerID, in.LegalEntityID); err != nil {
		return err
	}
	out, _, err := idem.Do(ctx, m.RT.DB, req, func(ctx context.Context, tx db.Tx) (idem.Outcome, error) {
		id := ids.New().String()
		tag, err := tx.Exec(ctx, `INSERT INTO credit_limit (id, partner_id, legal_entity_id, currency, amount)
			VALUES ($1, $2, $3, $4, $5::numeric) ON CONFLICT (partner_id, legal_entity_id, currency) DO NOTHING`,
			id, in.PartnerID, in.LegalEntityID, in.Currency, amt)
		if err != nil {
			return idem.Outcome{}, err
		}
		if tag.RowsAffected() == 0 {
			return idem.Outcome{}, core.ErrTaken.New("change the existing limit instead")
		}
		l, err := core.EmitLimit(m, ctx, tx, id)
		if err != nil {
			return idem.Outcome{}, err
		}
		if err := core.Change(m, ctx, tx, actor, core.OpLimitManage, core.Limits, l.ID, l); err != nil {
			return idem.Outcome{}, err
		}
		res, _ := json.Marshal(map[string]string{"id": l.ID})
		return idem.Outcome{Status: 200, Result: res}, nil
	})
	if err != nil {
		return err
	}
	var res struct{ ID string }
	_ = json.Unmarshal(out.Result, &res)
	l, err := limit(m, ctx, res.ID) // a replay answers with the limit as it is now
	if err != nil {
		return err
	}
	return c.JSON(200, l)
}

func limit(m *core.Base, ctx context.Context, id string) (core.Limit, error) {
	if !core.UUIDForm.MatchString(id) {
		return core.Limit{}, core.NotFound("limit")
	}
	var l core.Limit
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		l, err = core.GetLimit(ctx, tx, id, false)
		return err
	})
	if errors.Is(err, core.ErrNone) {
		return l, core.NotFound("limit")
	}
	return l, err
}

func GetLimitHTTP(m *core.Base, c *httpx.Context) error {
	l, err := limit(m, c.Ctx(), c.Param("id"))
	if err != nil {
		return err
	}
	if _, _, err := core.Check(c, core.OpLimitRead, l.LegalEntityID); err != nil {
		return err
	}
	return c.JSON(200, l)
}

// UpdateLimit changes the amount (it may go below what is used: only later holds see it), suspends or resumes.
func UpdateLimit(m *core.Base, c *httpx.Context) error {
	var in struct {
		Limit   json.RawMessage `json:"limit"`
		State   *string         `json:"state"`
		Version *int64          `json:"version"`
	}
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if in.State != nil && *in.State != "active" && *in.State != "suspended" {
		return core.Invalid("state", "state is active or suspended")
	}
	cur, err := limit(m, c.Ctx(), c.Param("id"))
	if err != nil {
		return err
	}
	ctx, actor, err := core.Check(c, core.OpLimitManage, cur.LegalEntityID)
	if err != nil {
		return err
	}
	setAmount, amt := len(in.Limit) > 0, (*string)(nil)
	if setAmount && string(in.Limit) != "null" {
		scale, err := masterdata.CurrencyScale(m, ctx, cur.Currency)
		if err != nil {
			return err
		}
		var s string
		if json.Unmarshal(in.Limit, &s) != nil {
			return core.Invalid("limit", "limit is a decimal string, or null")
		}
		v, err := core.Amount("limit", s, scale, true)
		if err != nil {
			return err
		}
		amt = &v
	}
	var out core.Limit
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		l, err := core.GetLimit(ctx, tx, cur.ID, true)
		if err != nil {
			return err
		}
		if in.Version != nil && *in.Version != l.Version {
			return core.ErrVersion.New(fmt.Sprintf("the limit is at version %d", l.Version))
		}
		next := l
		if setAmount {
			next.Limit = amt
		}
		if in.State != nil {
			next.State = *in.State
		}
		same := next.State == l.State && (next.Limit == nil) == (l.Limit == nil)
		if same && next.Limit != nil {
			if same, err = equal(ctx, tx, *next.Limit, *l.Limit); err != nil {
				return err
			}
		}
		if same {
			out = l
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE credit_limit SET amount = $2::numeric, state = $3, version = version + 1,
			updated_at = now() WHERE id = $1`, l.ID, next.Limit, next.State); err != nil {
			return err
		}
		if out, err = core.EmitLimit(m, ctx, tx, l.ID); err != nil {
			return err
		}
		return core.Change(m, ctx, tx, actor, core.OpLimitManage, core.Limits, out.ID, out)
	})
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}

func equal(ctx context.Context, tx db.Tx, a, b string) (bool, error) {
	var eq bool
	err := tx.QueryRow(ctx, `SELECT $1::numeric = $2::numeric`, a, b).Scan(&eq)
	return eq, err
}

// The lists of the user surface (protocol 14). Every field is ordinary; the legal entity compiles into the WHERE.
var (
	policyList = list.Spec{Operation: core.OpPolicyRead, Alias: "p", IDColumn: "legal_entity_id", IDKind: list.UUID}
	limitList  = list.Spec{Operation: core.OpLimitRead, Alias: "c", IDColumn: "id", IDKind: list.UUID,
		Columns: []list.Column{
			{Field: "partner_id", Column: "partner_id", Kind: list.UUID, Sortable: true, Filterable: true},
			{Field: "legal_entity_id", Column: "legal_entity_id", Kind: list.UUID, Sortable: true, Filterable: true},
			{Field: "currency", Column: "currency", Kind: list.Text, Sortable: true, Filterable: true},
			{Field: "state", Column: "state", Kind: list.Text, Filterable: true},
			{Field: "available", Column: "available", Kind: list.Decimal, Filterable: true}}}
)

func ListPolicies(m *core.Base, c *httpx.Context) error {
	limit, err := list.ParseLimit(c.Query("limit"))
	if err != nil {
		return err
	}
	s, _ := sdkauthz.From(c.Ctx())
	ctx, q, err := list.Build(c.Ctx(), s, policyList, list.Request{Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	out, next, err := list.Page(ctx, m.RT.DB, q, "credit_policy", core.PolicyCols, core.ScanPolicy,
		func(p core.Policy) ([]string, string) { return []string{}, p.LegalEntityID })
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"policies": out, "next_cursor": next})
}

func ListLimits(m *core.Base, c *httpx.Context) error {
	limit, err := list.ParseLimit(c.Query("limit"))
	if err != nil {
		return err
	}
	var f []list.Filter
	for _, k := range []string{"partner_id", "legal_entity_id", "currency", "state"} {
		v := c.Query(k)
		if v == "" {
			continue
		}
		if (k == "partner_id" || k == "legal_entity_id") && !core.UUIDForm.MatchString(v) {
			return core.Invalid(k, k+" is an ID")
		}
		f = append(f, list.Filter{Field: k, Op: "eq", Values: []string{v}})
	}
	if c.Query("over_limit") == "true" {
		f = append(f, list.Filter{Field: "available", Op: "lt", Values: []string{"0"}})
	}
	s, _ := sdkauthz.From(c.Ctx())
	ctx, q, err := list.Build(c.Ctx(), s, limitList, list.Request{Filters: f, Limit: limit, Cursor: c.Query("cursor"),
		Sort: []list.Order{{Field: "partner_id"}, {Field: "legal_entity_id"}, {Field: "currency"}}})
	if err != nil {
		return err
	}
	from := "(SELECT *, amount - order_exposure - receivable_exposure AS available FROM credit_limit)"
	out, next, err := list.Page(ctx, m.RT.DB, q, from, core.LimitCols, core.ScanLimit,
		func(l core.Limit) ([]string, string) { return []string{l.PartnerID, l.LegalEntityID, l.Currency}, l.ID })
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"limits": out, "next_cursor": next})
}

func Unsupported(m *core.Base, c *httpx.Context) error {
	l, err := limit(m, c.Ctx(), c.Param("id"))
	if err != nil {
		return err
	}
	if _, _, err := core.Check(c, core.OpLimitRequest, l.LegalEntityID); err != nil {
		return err
	}
	return errs.New(errs.ReasonCapabilityUnsupported, "temporary limits and exception releases are not in this version of erp/credit")
}
