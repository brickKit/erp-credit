package credit

import (
	"context"
	"encoding/json"
	"regexp"

	"github.com/brickKit/be-sdk-go/v3/audit"
	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/calls"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/errs"
	"github.com/brickKit/be-sdk-go/v3/grpcx"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/identity"
	"github.com/brickKit/be-sdk-go/v3/money"
	"github.com/brickKit/be-sdk-go/v3/runtime"
	creditv3 "github.com/brickKit/erp-credit/contract/v3/gen/go/be/erp/credit/v3"
	currencyv3 "github.com/brickKit/mdm-currency/contract/v3/gen/go/be/mdm/currency/v3"
	orgv3 "github.com/brickKit/mdm-org/contract/v3/gen/go/be/mdm/org/v3"
	partnerv3 "github.com/brickKit/mdm-partner/contract/v3/gen/go/be/mdm/partner/v3"
)

// The component's own reasons (the SDK's catalog has the shared ones).
var (
	errVersion = errs.Define(ID, "VERSION_CONFLICT", errs.Aborted, 409, errs.RetryNo, "The record changed since that version")
	errTaken   = errs.Define(ID, "LIMIT_EXISTS", errs.AlreadyExists, 409, errs.RetryNo,
		"The customer already has a limit in this legal entity and currency")
	errLegalEntity = errs.Define(ID, "LEGAL_ENTITY_NOT_USABLE", errs.FailedPrecondition, 409, errs.RetryNo,
		"The legal entity does not exist or is not active")
	errCustomer = errs.Define(ID, "CUSTOMER_NOT_ELIGIBLE", errs.FailedPrecondition, 409, errs.RetryNo,
		"The partner is not an active customer in this legal entity")
	errCurrency = errs.Define(ID, "CURRENCY_NOT_USABLE", errs.FailedPrecondition, 409, errs.RetryNo,
		"The currency does not exist or is not active")
	errContent = errs.Define(ID, "CONTENT_CONFLICT", errs.AlreadyExists, 409, errs.RetryNo,
		"The ID was used before with other content")
	errState = errs.Define(ID, "STATE_CONFLICT", errs.FailedPrecondition, 409, errs.RetryNo,
		"The branch's state does not allow this")
)

var (
	uuidForm     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	currencyForm = regexp.MustCompile(`^[A-Z]{3}$`)
)

// Module is the credit component.
type Module struct {
	creditv3.UnimplementedReservationServiceServer
	creditv3.UnimplementedCreditServiceServer
	rt *runtime.Runtime
}

// New builds the module from its runtime.
func New(rt *runtime.Runtime) (*Module, error) { return &Module{rt: rt}, nil }

// Routes registers the user surface (contract/openapi.yaml).
func (m *Module) Routes(r *httpx.Routes) error {
	add := func(method, path, op string, h httpx.Handler) {
		r.Add(httpx.Route{Method: method, Path: path, Operation: op, Handle: h})
	}
	add("GET", "/api/credit/policies", OpPolicyRead, m.listPolicies)
	add("GET", "/api/credit/policies/:legal_entity_id", OpPolicyRead, m.getPolicy)
	add("PUT", "/api/credit/policies/:legal_entity_id", OpPolicyManage, m.setPolicy)
	add("GET", "/api/credit/limits", OpLimitRead, m.listLimits)
	add("POST", "/api/credit/limits", OpLimitManage, m.createLimit)
	add("GET", "/api/credit/limits/:id", OpLimitRead, m.getLimitHTTP)
	add("PATCH", "/api/credit/limits/:id", OpLimitManage, m.updateLimit)
	add("GET", "/api/credit/branches/:id", OpLimitRead, m.getBranchHTTP)
	// in the contract, not in this version (D75)
	add("POST", "/api/credit/limits/:id/temporary-increases", OpLimitRequest, m.unsupported)
	return nil
}

func (m *Module) unsupported(c *httpx.Context) error {
	l, err := m.limit(c.Ctx(), c.Param("id"))
	if err != nil {
		return err
	}
	if _, _, err := check(c, OpLimitRequest, l.LegalEntityID); err != nil {
		return err
	}
	return errs.New(errs.ReasonCapabilityUnsupported, "temporary limits and exception releases are not in this version of erp/credit")
}

// Services registers the system surface: only the coordinator reserves (protocol 04).
func (m *Module) Services(s *grpcx.Services) error {
	s.Register(&creditv3.ReservationService_ServiceDesc, m, map[string]grpcx.Method{
		"Hold":    {Operation: OpSysReserve, Callers: []string{Sales}},
		"Confirm": {Operation: OpSysReserve, Callers: []string{Sales}},
		"Cancel":  {Operation: OpSysReserve, Callers: []string{Sales}},
		"Query":   {Operation: OpSysReserve, Callers: []string{Sales}},
		"Consume": {Operation: OpSysReserve, Callers: []string{Sales}},
		"Adjust":  {Operation: OpSysReserve, Callers: []string{Sales}}})
	s.Register(&creditv3.CreditService_ServiceDesc, m, map[string]grpcx.Method{
		"Available": {Operation: OpSysRead, Callers: []string{grpcx.AnyComponent}}})
	return nil
}

// check decides op on a record of the legal entity and returns the context to work in and the acting principal.
func check(c *httpx.Context, op, legalEntity string) (context.Context, string, error) {
	s, _ := sdkauthz.From(c.Ctx())
	ctx, _, err := s.Check(c.Ctx(), op, sdkauthz.Facts{DimLegalEntity: sdkauthz.Value(legalEntity)})
	if err != nil {
		return ctx, "", err
	}
	u, _ := identity.UserFrom(ctx)
	return ctx, u.Subject, nil
}

func invalid(field, why string) error {
	return errs.New(errs.ReasonRequestInvalid, why).WithViolations(errs.Violation{Field: field, Reason: "INVALID", Description: why})
}

func notFound(what string) error { return errs.New(errs.ReasonNotFound, what+" not found") }

// amount checks a decimal string: positive (or zero when zero is allowed) with at most scale places (the currency's
// minor unit, at most the columns' four), and returns its canonical form.
func amount(field, v string, scale int32, zeroOK bool) (string, error) {
	d, err := money.Parse(v)
	if err != nil || d.Sign() < 0 || d.Sign() == 0 && !zeroOK || -d.Exponent > scale || -d.Exponent > 4 {
		return "", invalid(field, field+" is a positive decimal string within the currency's minor unit")
	}
	d.Reduce(d)
	return d.Text('f'), nil
}

// change records the business change with the record's new state (protocol 03), in the transaction that made it.
func (m *Module) change(ctx context.Context, tx db.Tx, actor, op, typ, id string, after any) error {
	_, err := m.rt.Audit.Write(ctx, tx, audit.Record{Category: audit.BusinessChange, Operation: op, Outcome: audit.Succeeded,
		Actor: actor, Target: map[string]any{"type": typ, "id": id}, Changes: map[string]any{"after": after}})
	return err
}

// legalEntityActive asks mdm/org now, outside any transaction (protocol 07: unreachable is not "does not exist").
func (m *Module) legalEntityActive(ctx context.Context, id string) error {
	conn, ok := m.rt.GRPC(Org)
	if !ok {
		return errs.New(errs.ReasonDependencyUnavailable, "mdm/org is not reachable: the legal entity cannot be checked")
	}
	res, err := orgv3.NewSourceServiceClient(conn).Read(ctx, &orgv3.ReadRequest{
		Aggregate: orgv3.Aggregate_AGGREGATE_LEGAL_ENTITY, Ids: []string{id}}, calls.Idempotent())
	if err != nil {
		return err
	}
	if len(res.GetResults()) != 1 || res.GetResults()[0].GetState() != orgv3.ReadState_READ_STATE_EXISTS {
		return errLegalEntity.New("no legal entity " + id)
	}
	var le struct{ State string }
	if err := json.Unmarshal(res.GetResults()[0].GetData(), &le); err != nil {
		return err
	}
	if le.State != "active" {
		return errLegalEntity.New("the legal entity is " + le.State)
	}
	return nil
}

// customerEligible asks mdm/partner whether the partner may be sold to in the legal entity (protocol 07).
func (m *Module) customerEligible(ctx context.Context, partner, legalEntity string) error {
	conn, ok := m.rt.GRPC(Partner)
	if !ok {
		return errs.New(errs.ReasonDependencyUnavailable, "mdm/partner is not reachable: the customer cannot be checked")
	}
	res, err := partnerv3.NewPartnerServiceClient(conn).CheckEligibility(ctx, &partnerv3.CheckEligibilityRequest{
		PartnerId: partner, LegalEntityId: legalEntity, Purpose: partnerv3.Purpose_PURPOSE_SELL}, calls.Idempotent())
	if err != nil {
		return err
	}
	if !res.GetEligible() {
		return errCustomer.New("the partner is not eligible to sell to: " + res.GetReason())
	}
	return nil
}

// currencyScale asks mdm/currency for an active currency's minor unit.
func (m *Module) currencyScale(ctx context.Context, code string) (int32, error) {
	conn, ok := m.rt.GRPC(Currency)
	if !ok {
		return 0, errs.New(errs.ReasonDependencyUnavailable, "mdm/currency is not reachable: the currency cannot be checked")
	}
	res, err := currencyv3.NewSourceServiceClient(conn).Read(ctx, &currencyv3.ReadRequest{
		Aggregate: currencyv3.Aggregate_AGGREGATE_CURRENCY, Ids: []string{code}}, calls.Idempotent())
	if err != nil {
		return 0, err
	}
	if len(res.GetResults()) != 1 || res.GetResults()[0].GetState() != currencyv3.ReadState_READ_STATE_EXISTS {
		return 0, errCurrency.New("no currency " + code)
	}
	var c struct {
		MinorUnit int32 `json:"minor_unit"`
		Active    bool  `json:"active"`
	}
	if err := json.Unmarshal(res.GetResults()[0].GetData(), &c); err != nil {
		return 0, err
	}
	if !c.Active {
		return 0, errCurrency.New("currency " + code + " is not active")
	}
	return c.MinorUnit, nil
}
