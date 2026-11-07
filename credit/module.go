package credit

import (
	"context"
	"time"

	"github.com/brickKit/be-sdk-go/v3/grpcx"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/runtime"
	"github.com/brickKit/be-sdk-go/v3/work"
	creditv3 "github.com/brickKit/erp-credit/contract/v3/gen/go/be/erp/credit/v3"
	branches "github.com/brickKit/erp-credit/v3/internal/branches"
	core "github.com/brickKit/erp-credit/v3/internal/core"
	limits "github.com/brickKit/erp-credit/v3/internal/limits"
)

// Module is the credit component as the launcher and the shells run it: it only assembles the business modules under
// internal/ (their map is internal/AGENTS.md); the gRPC services answer through them (services.go).
type Module struct {
	creditv3.UnimplementedReservationServiceServer
	creditv3.UnimplementedCreditServiceServer
	b *core.Base
}

// New builds the module from its runtime.
func New(rt *runtime.Runtime) (*Module, error) { return &Module{b: &core.Base{RT: rt}}, nil }

// Routes registers the user surface (contract/openapi.yaml).
func (m *Module) Routes(r *httpx.Routes) error {
	add := func(method, path, op string, h httpx.Handler) {
		r.Add(httpx.Route{Method: method, Path: path, Operation: op, Handle: h})
	}
	h := m.handler
	add("GET", "/api/credit/policies", core.OpPolicyRead, h(limits.ListPolicies))
	add("GET", "/api/credit/policies/:legal_entity_id", core.OpPolicyRead, h(limits.GetPolicy))
	add("PUT", "/api/credit/policies/:legal_entity_id", core.OpPolicyManage, h(limits.SetPolicy))
	add("GET", "/api/credit/limits", core.OpLimitRead, h(limits.ListLimits))
	add("POST", "/api/credit/limits", core.OpLimitManage, h(limits.CreateLimit))
	add("GET", "/api/credit/limits/:id", core.OpLimitRead, h(limits.GetLimitHTTP))
	add("PATCH", "/api/credit/limits/:id", core.OpLimitManage, h(limits.UpdateLimit))
	add("GET", "/api/credit/branches/:id", core.OpLimitRead, h(branches.GetBranchHTTP))
	// in the contract, not in this version (D75)
	add("POST", "/api/credit/limits/:id/temporary-increases", core.OpLimitRequest, h(limits.Unsupported))
	return nil
}

// Services registers the system surface: the reservation branches for erp/sales (protocol 04), the available credit
// for any component.
func (m *Module) Services(s *grpcx.Services) error {
	s.Register(&creditv3.ReservationService_ServiceDesc, m, map[string]grpcx.Method{
		"Hold":    {Operation: core.OpSysReserve, Callers: []string{core.Sales}},
		"Confirm": {Operation: core.OpSysReserve, Callers: []string{core.Sales}},
		"Cancel":  {Operation: core.OpSysReserve, Callers: []string{core.Sales}},
		"Query":   {Operation: core.OpSysReserve, Callers: []string{core.Sales}},
		"Consume": {Operation: core.OpSysReserve, Callers: []string{core.Sales}},
		"Adjust":  {Operation: core.OpSysReserve, Callers: []string{core.Sales}}})
	s.Register(&creditv3.CreditService_ServiceDesc, m, map[string]grpcx.Method{
		"Available": {Operation: core.OpSysRead, Callers: []string{grpcx.AnyComponent}}})
	return nil
}

// Jobs declares the background work: the expiry of holds (protocol 01).
func (m *Module) Jobs(j *work.Jobs) error {
	j.Add(work.Job{Name: "expire-holds", Kind: work.Singleton, Effect: work.Local, Interval: 10 * time.Second,
		MaxAttempts: work.Unlimited, Handle: func(ctx context.Context, r *work.Run) error { return branches.ExpireHolds(m.b, ctx, r) }})
	return nil
}

// handler adapts a module's handler, which takes the shared base first, to what the SDK registers.
func (m *Module) handler(f func(*core.Base, *httpx.Context) error) httpx.Handler {
	return func(c *httpx.Context) error { return f(m.b, c) }
}
