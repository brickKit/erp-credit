package credit

import (
	"context"

	creditv3 "github.com/brickKit/erp-credit/contract/v3/gen/go/be/erp/credit/v3"
	branches "github.com/brickKit/erp-credit/v3/internal/branches"
)

// The gRPC methods of the system surface answer through internal/branches.

// Hold is ReservationService.Hold.
func (m *Module) Hold(ctx context.Context, in *creditv3.HoldRequest) (*creditv3.HoldResponse, error) {
	return branches.Hold(m.b, ctx, in)
}

// Confirm is ReservationService.Confirm.
func (m *Module) Confirm(ctx context.Context, in *creditv3.ConfirmRequest) (*creditv3.ConfirmResponse, error) {
	return branches.Confirm(m.b, ctx, in)
}

// Cancel is ReservationService.Cancel.
func (m *Module) Cancel(ctx context.Context, in *creditv3.CancelRequest) (*creditv3.CancelResponse, error) {
	return branches.Cancel(m.b, ctx, in)
}

// Query is ReservationService.Query.
func (m *Module) Query(ctx context.Context, in *creditv3.QueryRequest) (*creditv3.QueryResponse, error) {
	return branches.Query(m.b, ctx, in)
}

// Consume is ReservationService.Consume.
func (m *Module) Consume(ctx context.Context, in *creditv3.ConsumeRequest) (*creditv3.ConsumeResponse, error) {
	return branches.Consume(m.b, ctx, in)
}

// Adjust is ReservationService.Adjust.
func (m *Module) Adjust(ctx context.Context, in *creditv3.AdjustRequest) (*creditv3.AdjustResponse, error) {
	return branches.Adjust(m.b, ctx, in)
}

// Available is CreditService.Available.
func (m *Module) Available(ctx context.Context, in *creditv3.AvailableRequest) (*creditv3.AvailableResponse, error) {
	return branches.Available(m.b, ctx, in)
}
