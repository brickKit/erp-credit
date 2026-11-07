//go:build integration

package itest

import (
	"sync"

	"github.com/brickKit/be-sdk-go/v3/ids"
	creditv3 "github.com/brickKit/erp-credit/contract/v3/gen/go/be/erp/credit/v3"
	core "github.com/brickKit/erp-credit/v3/internal/core"
	assert "github.com/stretchr/testify/assert"
	require "github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// racingHolds: the boundary card's story: two orders confirm at once, only the one within the limit succeeds;
// ten more race for the rest, exactly eight are held; each refusal over the limit is a fact; the limit is per currency.
func (fx *fixture) racingHolds() {
	t, ctx := fx.t, fx.ctx
	// the boundary card's story: two orders confirm at once, only the one within the limit succeeds
	var wg sync.WaitGroup
	var mu sync.Mutex
	got := map[creditv3.BranchState]int{}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := fx.hold(ids.New().String(), fx.cust, "CNY", "600")
			mu.Lock()
			got[b.State]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	assert.Equal(t, 1, got[creditv3.BranchState_BRANCH_STATE_HELD], got)
	assert.Equal(t, 1, got[creditv3.BranchState_BRANCH_STATE_REJECTED], got)
	// ten more race for the remaining 400, 50 each: exactly eight are held
	got = map[creditv3.BranchState]int{}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := fx.hold(ids.New().String(), fx.cust, "CNY", "50")
			mu.Lock()
			got[b.State]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	assert.Equal(t, 8, got[creditv3.BranchState_BRANCH_STATE_HELD], got)
	_, l := fx.alice.do("GET", "/api/credit/limits/"+fx.lim["id"].(string), nil)
	assert.Equal(t, "0", l["available"])
	var exceeded int
	require.NoError(t, fx.admin.QueryRow(ctx, "SELECT count(*) FROM "+itSchema+".be_outbox WHERE type = $1", core.LimitExceeded).Scan(&exceeded))
	assert.Equal(t, 3, exceeded, "each refusal over the limit is a fact for the credit manager")
	// the limit is per currency: no conversion, so the customer has no USD limit and the policy decides
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_HELD, fx.hold(ids.New().String(), fx.cust, "JPY", "5").State, "unlimited policy")
}

// branchLifecycle: raise the limit; the same hold again answers the first, other content is refused (RS17); a
// confirmed branch never expires, a consume takes off it (once per consumption ID), a cancel gives back the rest.
func (fx *fixture) branchLifecycle() {
	t, ctx := fx.t, fx.ctx
	// raise the limit; a confirmed branch never expires, a consume takes off it, a cancel gives back the rest
	code, l := fx.alice.do("PATCH", "/api/credit/limits/"+fx.lim["id"].(string), map[string]any{"limit": "2000"})
	require.Equal(t, 200, code, l)
	order := ids.New().String()
	require.Equal(t, creditv3.BranchState_BRANCH_STATE_HELD, fx.hold(order, fx.cust, "CNY", "300").State)
	again := fx.hold(order, fx.cust, "CNY", "300")
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_HELD, again.State, "the same hold again: the first answer")
	_, err := fx.res.Hold(ctx, &creditv3.HoldRequest{BranchId: order, PartnerId: fx.cust, LegalEntityId: fx.le, Currency: "CNY", Amount: "301",
		Reference: &creditv3.Reference{Kind: "sales_order", Id: "SO-" + order[:8], Revision: 1}})
	assert.Equal(t, codes.AlreadyExists, status.Code(err), "RS17")
	_, l = fx.alice.do("GET", "/api/credit/limits/"+fx.lim["id"].(string), nil)
	assert.Equal(t, "700", l["available"], "held once")
	cf, err := fx.res.Confirm(ctx, &creditv3.ConfirmRequest{BranchId: order})
	require.NoError(t, err)
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_COMMITTED, cf.Branch.State)
	cs, err := fx.res.Consume(ctx, &creditv3.ConsumeRequest{ConsumptionId: "INV-1", BranchId: order, Amount: "100"})
	require.NoError(t, err)
	assert.Equal(t, "100", cs.Branch.Consumed)
	_, err = fx.res.Consume(ctx, &creditv3.ConsumeRequest{ConsumptionId: "INV-1", BranchId: order, Amount: "100"})
	require.NoError(t, err, "the same consume again is harmless")
	cc, err := fx.res.Cancel(ctx, &creditv3.CancelRequest{BranchId: order})
	require.NoError(t, err)
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_RELEASED, cc.Branch.State)
	_, l = fx.alice.do("GET", "/api/credit/limits/"+fx.lim["id"].(string), nil)
	assert.Equal(t, "1000", l["available"], "the consumed 100 left the order exposure, the other 200 came back")
}

// lateAndExpired: a cancel before the hold leaves a tombstone, the late hold is refused (RS04); an unknown branch is
// absent; an expired hold is not revived.
func (fx *fixture) lateAndExpired() {
	t, ctx := fx.t, fx.ctx
	// RS04: a cancel before the hold leaves a tombstone; the late hold is refused
	late := ids.New().String()
	_, err := fx.res.Cancel(ctx, &creditv3.CancelRequest{BranchId: late})
	require.NoError(t, err)
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_CANCELLED, fx.hold(late, fx.cust, "CNY", "10").State)
	q, err := fx.res.Query(ctx, &creditv3.QueryRequest{BranchId: ids.New().String()})
	require.NoError(t, err)
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_ABSENT, q.Branch.State)

	// an expired hold is not revived
	stale := ids.New().String()
	fx.hold(stale, fx.cust, "CNY", "10")
	_, err = fx.admin.Exec(ctx, "UPDATE "+itSchema+".branch SET hold_expires_at = now() - interval '1 second' WHERE id = $1", stale)
	require.NoError(t, err)
	cf, err := fx.res.Confirm(ctx, &creditv3.ConfirmRequest{BranchId: stale})
	require.NoError(t, err)
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_EXPIRED, cf.Branch.State)
}
