//go:build integration

package itest

import (
	"github.com/brickKit/be-sdk-go/v3/ids"
	creditv3 "github.com/brickKit/erp-credit/contract/v3/gen/go/be/erp/credit/v3"
	assert "github.com/stretchr/testify/assert"
	require "github.com/stretchr/testify/require"
)

// policies: a legal entity without a policy blocks a customer without a limit; under an unlimited policy the
// customer is held and counted, never refused, and a limit row without an amount appears.
func (fx *fixture) policies() {
	t := fx.t
	// a legal entity without a policy blocks a customer without a limit
	b := fx.hold(ids.New().String(), fx.cash, "CNY", "100")
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_REJECTED, b.State)
	assert.Equal(t, "no_limit", b.Reason)
	code, pol := fx.alice.do("PUT", "/api/credit/policies/"+fx.le, map[string]any{"without_limit": "unlimited"})
	require.Equal(t, 200, code, pol)
	// unlimited: the customer is held and counted, never refused, and a limit row without an amount appears
	b = fx.hold(ids.New().String(), fx.cash, "CNY", "99999999")
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_HELD, b.State)
	_, lims := fx.alice.do("GET", "/api/credit/limits?partner_id="+fx.cash, nil)
	cashLimit := lims["limits"].([]any)[0].(map[string]any)
	assert.Nil(t, cashLimit["limit"])
	assert.Equal(t, "99999999", cashLimit["order_exposure"])
}
