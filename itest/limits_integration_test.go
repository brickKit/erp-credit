//go:build integration

package itest

import (
	"github.com/brickKit/be-sdk-go/v3/ids"
	creditv3 "github.com/brickKit/erp-credit/contract/v3/gen/go/be/erp/credit/v3"
	assert "github.com/stretchr/testify/assert"
	require "github.com/stretchr/testify/require"
)

// limits: the partner must be a customer of the legal entity, the currency active, the amount in its minor unit;
// one limit a customer, legal entity and currency.
func (fx *fixture) limits() {
	t := fx.t
	// limits: the partner must be a customer of the legal entity, the currency active, the amount in its minor unit
	code, bad := fx.alice.do("POST", "/api/credit/limits", map[string]any{"partner_id": fx.stranger, "legal_entity_id": fx.le, "currency": "CNY", "limit": "1000"})
	assert.Equal(t, 409, code)
	assert.Equal(t, "CUSTOMER_NOT_ELIGIBLE", bad["reason"])
	code, bad = fx.alice.do("POST", "/api/credit/limits", map[string]any{"partner_id": fx.cust, "legal_entity_id": fx.le, "currency": "ITL", "limit": "1000"})
	assert.Equal(t, 409, code)
	assert.Equal(t, "CURRENCY_NOT_USABLE", bad["reason"])
	code, _ = fx.alice.do("POST", "/api/credit/limits", map[string]any{"partner_id": fx.cust, "legal_entity_id": fx.le, "currency": "JPY", "limit": "1000.5"})
	assert.Equal(t, 400, code, "yen has no minor unit")
	code, lim := fx.alice.do("POST", "/api/credit/limits", map[string]any{"partner_id": fx.cust, "legal_entity_id": fx.le, "currency": "CNY", "limit": "1000.00"})
	require.Equal(t, 200, code, lim)
	assert.Equal(t, "1000", lim["limit"])
	code, _ = fx.alice.do("POST", "/api/credit/limits", map[string]any{"partner_id": fx.cust, "legal_entity_id": fx.le, "currency": "CNY", "limit": "1"})
	assert.Equal(t, 409, code, "one limit a customer, legal entity and currency")
	fx.lim = lim
}

// suspendedLimit: a suspended limit takes no new hold; its committed branches go on; the available credit for a
// coordinator.
func (fx *fixture) suspendedLimit() {
	t, ctx := fx.t, fx.ctx
	// a suspended limit takes no new hold; its committed branches go on
	_, _ = fx.alice.do("PATCH", "/api/credit/limits/"+fx.lim["id"].(string), map[string]any{"state": "suspended"})
	b := fx.hold(ids.New().String(), fx.cust, "CNY", "1")
	assert.Equal(t, "limit_suspended", b.Reason)

	av, err := creditv3.NewCreditServiceClient(fx.conn).Available(ctx, &creditv3.AvailableRequest{PartnerId: fx.cust, LegalEntityId: fx.le, Currency: "CNY"})
	require.NoError(t, err)
	assert.True(t, av.HasLimit)
	assert.Equal(t, "2000", av.Limit)
	assert.Equal(t, "suspended", av.State)
	assert.False(t, av.ReceivablesTracked)
}
