package credit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/idem"
	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/list"
)

func policyOf(ctx context.Context, tx db.Tx, legalEntity string, lock bool) (Policy, error) {
	q := `SELECT ` + policyCols + ` FROM credit_policy p WHERE p.legal_entity_id = $1::uuid`
	if lock {
		q += ` FOR UPDATE`
	}
	p, err := one(tx.QueryRow(ctx, q, legalEntity), scanPolicy)
	if errors.Is(err, errNone) { // no row is block (docs/design.md)
		return Policy{ID: legalEntity, LegalEntityID: legalEntity, WithoutLimit: "block", Version: 0}, nil
	}
	return p, err
}

func (m *Module) getPolicy(c *httpx.Context) error {
	le := c.Param("legal_entity_id")
	if !uuidForm.MatchString(le) {
		return notFound("policy")
	}
	ctx, _, err := check(c, OpPolicyRead, le)
	if err != nil {
		return err
	}
	var p Policy
	err = m.rt.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		p, err = policyOf(ctx, tx, le, false)
		return err
	})
	if err != nil {
		return err
	}
	return c.JSON(200, p)
}

// setPolicy sets a legal entity's policy for customers without a limit; the legal entity must be active now.
func (m *Module) setPolicy(c *httpx.Context) error {
	le := c.Param("legal_entity_id")
	if !uuidForm.MatchString(le) {
		return notFound("policy")
	}
	var in struct {
		WithoutLimit string `json:"without_limit"`
		Version      *int64 `json:"version"`
	}
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if in.WithoutLimit != "block" && in.WithoutLimit != "unlimited" {
		return invalid("without_limit", "without_limit is block or unlimited")
	}
	ctx, actor, err := check(c, OpPolicyManage, le)
	if err != nil {
		return err
	}
	if err := m.legalEntityActive(ctx, le); err != nil {
		return err
	}
	var out Policy
	err = m.rt.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO credit_policy (legal_entity_id, without_limit, version) VALUES ($1, 'block', 0)
			ON CONFLICT DO NOTHING`, le); err != nil {
			return err
		}
		cur, err := policyOf(ctx, tx, le, true)
		if err != nil {
			return err
		}
		if in.Version != nil && *in.Version != cur.Version {
			return errVersion.New(fmt.Sprintf("the policy is at version %d", cur.Version))
		}
		if cur.WithoutLimit == in.WithoutLimit && cur.Version > 0 {
			out = cur
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE credit_policy SET without_limit = $2, version = version + 1, updated_at = now()
			WHERE legal_entity_id = $1`, le, in.WithoutLimit); err != nil {
			return err
		}
		if out, err = policyOf(ctx, tx, le, false); err != nil {
			return err
		}
		if err := m.emit(ctx, tx, PolicyChanged, out.ID, out.Version, out); err != nil {
			return err
		}
		return m.change(ctx, tx, actor, OpPolicyManage, Policies, out.ID, out)
	})
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}

// createLimit sets a customer's limit, once per idempotency key; the partner must be an active customer of the legal
// entity and the currency active. limit null puts the customer outside credit control.
func (m *Module) createLimit(c *httpx.Context) error {
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
	case !uuidForm.MatchString(in.PartnerID):
		return invalid("partner_id", "partner_id is a partner's ID")
	case !uuidForm.MatchString(in.LegalEntityID):
		return invalid("legal_entity_id", "legal_entity_id is a legal entity's ID")
	case !currencyForm.MatchString(in.Currency):
		return invalid("currency", "currency is an ISO 4217 code")
	case len(in.Limit) == 0:
		return invalid("limit", "limit is required: a decimal string, or null for no credit control")
	}
	ctx, actor, err := check(c, OpLimitManage, in.LegalEntityID)
	if err != nil {
		return err
	}
	scale, err := m.currencyScale(ctx, in.Currency)
	if err != nil {
		return err
	}
	var amt *string
	if string(in.Limit) != "null" {
		var s string
		if json.Unmarshal(in.Limit, &s) != nil {
			return invalid("limit", "limit is a decimal string, or null")
		}
		v, err := amount("limit", s, scale, true)
		if err != nil {
			return err
		}
		amt = &v
	}
	if err := m.customerEligible(ctx, in.PartnerID, in.LegalEntityID); err != nil {
		return err
	}
	out, _, err := idem.Do(ctx, m.rt.DB, req, func(ctx context.Context, tx db.Tx) (idem.Outcome, error) {
		id := ids.New().String()
		tag, err := tx.Exec(ctx, `INSERT INTO credit_limit (id, partner_id, legal_entity_id, currency, amount)
			VALUES ($1, $2, $3, $4, $5::numeric) ON CONFLICT (partner_id, legal_entity_id, currency) DO NOTHING`,
			id, in.PartnerID, in.LegalEntityID, in.Currency, amt)
		if err != nil {
			return idem.Outcome{}, err
		}
		if tag.RowsAffected() == 0 {
			return idem.Outcome{}, errTaken.New("change the existing limit instead")
		}
		l, err := m.emitLimit(ctx, tx, id)
		if err != nil {
			return idem.Outcome{}, err
		}
		if err := m.change(ctx, tx, actor, OpLimitManage, Limits, l.ID, l); err != nil {
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
	l, err := m.limit(ctx, res.ID) // a replay answers with the limit as it is now
	if err != nil {
		return err
	}
	return c.JSON(200, l)
}

func (m *Module) limit(ctx context.Context, id string) (Limit, error) {
	if !uuidForm.MatchString(id) {
		return Limit{}, notFound("limit")
	}
	var l Limit
	err := m.rt.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		l, err = getLimit(ctx, tx, id, false)
		return err
	})
	if errors.Is(err, errNone) {
		return l, notFound("limit")
	}
	return l, err
}

func (m *Module) getLimitHTTP(c *httpx.Context) error {
	l, err := m.limit(c.Ctx(), c.Param("id"))
	if err != nil {
		return err
	}
	if _, _, err := check(c, OpLimitRead, l.LegalEntityID); err != nil {
		return err
	}
	return c.JSON(200, l)
}

// updateLimit changes the amount (it may go below what is used: only later holds see it), suspends or resumes.
func (m *Module) updateLimit(c *httpx.Context) error {
	var in struct {
		Limit   json.RawMessage `json:"limit"`
		State   *string         `json:"state"`
		Version *int64          `json:"version"`
	}
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if in.State != nil && *in.State != "active" && *in.State != "suspended" {
		return invalid("state", "state is active or suspended")
	}
	cur, err := m.limit(c.Ctx(), c.Param("id"))
	if err != nil {
		return err
	}
	ctx, actor, err := check(c, OpLimitManage, cur.LegalEntityID)
	if err != nil {
		return err
	}
	setAmount, amt := len(in.Limit) > 0, (*string)(nil)
	if setAmount && string(in.Limit) != "null" {
		scale, err := m.currencyScale(ctx, cur.Currency)
		if err != nil {
			return err
		}
		var s string
		if json.Unmarshal(in.Limit, &s) != nil {
			return invalid("limit", "limit is a decimal string, or null")
		}
		v, err := amount("limit", s, scale, true)
		if err != nil {
			return err
		}
		amt = &v
	}
	var out Limit
	err = m.rt.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		l, err := getLimit(ctx, tx, cur.ID, true)
		if err != nil {
			return err
		}
		if in.Version != nil && *in.Version != l.Version {
			return errVersion.New(fmt.Sprintf("the limit is at version %d", l.Version))
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
		if out, err = m.emitLimit(ctx, tx, l.ID); err != nil {
			return err
		}
		return m.change(ctx, tx, actor, OpLimitManage, Limits, out.ID, out)
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
	policyList = list.Spec{Operation: OpPolicyRead, Alias: "p", IDColumn: "legal_entity_id", IDKind: list.UUID}
	limitList  = list.Spec{Operation: OpLimitRead, Alias: "c", IDColumn: "id", IDKind: list.UUID,
		Columns: []list.Column{
			{Field: "partner_id", Column: "partner_id", Kind: list.UUID, Sortable: true, Filterable: true},
			{Field: "legal_entity_id", Column: "legal_entity_id", Kind: list.UUID, Sortable: true, Filterable: true},
			{Field: "currency", Column: "currency", Kind: list.Text, Sortable: true, Filterable: true},
			{Field: "state", Column: "state", Kind: list.Text, Filterable: true},
			{Field: "available", Column: "available", Kind: list.Decimal, Filterable: true}}}
)

func (m *Module) listPolicies(c *httpx.Context) error {
	limit, err := list.ParseLimit(c.Query("limit"))
	if err != nil {
		return err
	}
	s, _ := sdkauthz.From(c.Ctx())
	ctx, q, err := list.Build(c.Ctx(), s, policyList, list.Request{Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	out, next, err := list.Page(ctx, m.rt.DB, q, "credit_policy", policyCols, scanPolicy,
		func(p Policy) ([]string, string) { return []string{}, p.LegalEntityID })
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"policies": out, "next_cursor": next})
}

func (m *Module) listLimits(c *httpx.Context) error {
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
		if (k == "partner_id" || k == "legal_entity_id") && !uuidForm.MatchString(v) {
			return invalid(k, k+" is an ID")
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
	out, next, err := list.Page(ctx, m.rt.DB, q, from, limitCols, scanLimit,
		func(l Limit) ([]string, string) { return []string{l.PartnerID, l.LegalEntityID, l.Currency}, l.ID })
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"limits": out, "next_cursor": next})
}
