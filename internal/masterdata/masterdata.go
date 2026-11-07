package masterdata

import (
	"context"
	"encoding/json"

	core "github.com/brickKit/erp-credit/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/calls"
	"github.com/brickKit/be-sdk-go/v3/errs"
	currencyv3 "github.com/brickKit/mdm-currency/contract/v3/gen/go/be/mdm/currency/v3"
	orgv3 "github.com/brickKit/mdm-org/contract/v3/gen/go/be/mdm/org/v3"
	partnerv3 "github.com/brickKit/mdm-partner/contract/v3/gen/go/be/mdm/partner/v3"
)

// LegalEntityActive asks mdm/org now, outside any transaction (protocol 07: unreachable is not "does not exist").
func LegalEntityActive(m *core.Base, ctx context.Context, id string) error {
	conn, ok := m.RT.GRPC(core.Org)
	if !ok {
		return errs.New(errs.ReasonDependencyUnavailable, "mdm/org is not reachable: the legal entity cannot be checked")
	}
	res, err := orgv3.NewSourceServiceClient(conn).Read(ctx, &orgv3.ReadRequest{
		Aggregate: orgv3.Aggregate_AGGREGATE_LEGAL_ENTITY, Ids: []string{id}}, calls.Idempotent())
	if err != nil {
		return err
	}
	if len(res.GetResults()) != 1 || res.GetResults()[0].GetState() != orgv3.ReadState_READ_STATE_EXISTS {
		return core.ErrLegalEntity.New("no legal entity " + id)
	}
	var le struct{ State string }
	if err := json.Unmarshal(res.GetResults()[0].GetData(), &le); err != nil {
		return err
	}
	if le.State != "active" {
		return core.ErrLegalEntity.New("the legal entity is " + le.State)
	}
	return nil
}

// CustomerEligible asks mdm/partner whether the partner may be sold to in the legal entity (protocol 07).
func CustomerEligible(m *core.Base, ctx context.Context, partner, legalEntity string) error {
	conn, ok := m.RT.GRPC(core.Partner)
	if !ok {
		return errs.New(errs.ReasonDependencyUnavailable, "mdm/partner is not reachable: the customer cannot be checked")
	}
	res, err := partnerv3.NewPartnerServiceClient(conn).CheckEligibility(ctx, &partnerv3.CheckEligibilityRequest{
		PartnerId: partner, LegalEntityId: legalEntity, Purpose: partnerv3.Purpose_PURPOSE_SELL}, calls.Idempotent())
	if err != nil {
		return err
	}
	if !res.GetEligible() {
		return core.ErrCustomer.New("the partner is not eligible to sell to: " + res.GetReason())
	}
	return nil
}

// CurrencyScale asks mdm/currency for an active currency's minor unit.
func CurrencyScale(m *core.Base, ctx context.Context, code string) (int32, error) {
	conn, ok := m.RT.GRPC(core.Currency)
	if !ok {
		return 0, errs.New(errs.ReasonDependencyUnavailable, "mdm/currency is not reachable: the currency cannot be checked")
	}
	res, err := currencyv3.NewSourceServiceClient(conn).Read(ctx, &currencyv3.ReadRequest{
		Aggregate: currencyv3.Aggregate_AGGREGATE_CURRENCY, Ids: []string{code}}, calls.Idempotent())
	if err != nil {
		return 0, err
	}
	if len(res.GetResults()) != 1 || res.GetResults()[0].GetState() != currencyv3.ReadState_READ_STATE_EXISTS {
		return 0, core.ErrCurrency.New("no currency " + code)
	}
	var c struct {
		MinorUnit int32 `json:"minor_unit"`
		Active    bool  `json:"active"`
	}
	if err := json.Unmarshal(res.GetResults()[0].GetData(), &c); err != nil {
		return 0, err
	}
	if !c.Active {
		return 0, core.ErrCurrency.New("currency " + code + " is not active")
	}
	return c.MinorUnit, nil
}
