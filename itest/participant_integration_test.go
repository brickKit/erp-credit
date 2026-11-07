//go:build integration

package itest

import (
	"time"

	"github.com/brickKit/be-sdk-go/v3/ids"
	creditv3 "github.com/brickKit/erp-credit/contract/v3/gen/go/be/erp/credit/v3"
	assert "github.com/stretchr/testify/assert"
	require "github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// participant: the cells of protocol 04's branch transitions the business flows do not reach, each answered as it is
// today (the reservation-participant plan's R0): a confirm or a cancel on a branch that ended changes nothing; only a
// committed branch is consumed, never beyond what is left, and consuming all of it makes it consumed; a consumption ID
// used with another amount is a conflict; a query leaves no tombstone; the participant's own job expires a hold past
// its period; an adjustment is not in this version. The available credit is the same before and after. It runs before the limit is suspended.
func (fx *fixture) participant() {
	t, ctx := fx.t, fx.ctx
	available := func() string {
		_, l := fx.alice.do("GET", "/api/credit/limits/"+fx.lim["id"].(string), nil)
		return l["available"].(string)
	}
	confirm := func(id string) (*creditv3.Branch, error) {
		r, err := fx.res.Confirm(ctx, &creditv3.ConfirmRequest{BranchId: id})
		return r.GetBranch(), err
	}
	cancel := func(id string) *creditv3.Branch {
		t.Helper()
		r, err := fx.res.Cancel(ctx, &creditv3.CancelRequest{BranchId: id})
		require.NoError(t, err)
		return r.GetBranch()
	}
	consume := func(id, consumption, amount string) (*creditv3.Branch, error) {
		r, err := fx.res.Consume(ctx, &creditv3.ConsumeRequest{ConsumptionId: consumption, BranchId: id, Amount: amount})
		return r.GetBranch(), err
	}
	refused := func(err error, code codes.Code, words string) {
		t.Helper()
		require.Error(t, err)
		assert.Equal(t, code, status.Code(err), err)
		assert.Contains(t, status.Convert(err).Message(), words)
	}
	before := available()

	// a query leaves no tombstone: the hold after it is held
	fresh := ids.New().String()
	q, err := fx.res.Query(ctx, &creditv3.QueryRequest{BranchId: fresh})
	require.NoError(t, err)
	require.Equal(t, creditv3.BranchState_BRANCH_STATE_ABSENT, q.Branch.State)
	require.Equal(t, creditv3.BranchState_BRANCH_STATE_HELD, fx.hold(fresh, fx.cust, "CNY", "40").State)

	// held: not consumed; confirmed twice, it stays committed
	_, err = consume(fresh, "R0-1", "10")
	refused(err, codes.FailedPrecondition, "only a committed branch is consumed; it is held")
	b, err := confirm(fresh)
	require.NoError(t, err)
	require.Equal(t, creditv3.BranchState_BRANCH_STATE_COMMITTED, b.State)
	b, err = confirm(fresh)
	require.NoError(t, err)
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_COMMITTED, b.State, "confirming again answers committed")
	version := b.Version

	// committed: consumed in parts, never beyond what is left; the same consumption ID with another amount conflicts
	b, err = consume(fresh, "R0-1", "10")
	require.NoError(t, err)
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_COMMITTED, b.State, "something is left")
	assert.Greater(t, b.Version, version)
	_, err = consume(fresh, "R0-1", "11")
	refused(err, codes.AlreadyExists, "consumption R0-1 was used with other content")
	_, err = consume(fresh, "R0-2", "31")
	refused(err, codes.FailedPrecondition, "more than the branch has left")
	b, err = consume(fresh, "R0-2", "30")
	require.NoError(t, err)
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_CONSUMED, b.State, "all of it consumed")
	assert.Equal(t, "40", b.Consumed)

	// consumed: a confirm, a cancel and a consume change nothing
	b, err = confirm(fresh)
	require.NoError(t, err)
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_CONSUMED, b.State)
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_CONSUMED, cancel(fresh).State)
	_, err = consume(fresh, "R0-3", "1")
	refused(err, codes.FailedPrecondition, "only a committed branch is consumed; it is consumed")

	// released: a confirm and a cancel again change nothing
	released := ids.New().String()
	fx.hold(released, fx.cust, "CNY", "10")
	require.Equal(t, creditv3.BranchState_BRANCH_STATE_RELEASED, cancel(released).State)
	b, err = confirm(released)
	require.NoError(t, err)
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_RELEASED, b.State, "not revived")
	again := cancel(released)
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_RELEASED, again.State)
	assert.Equal(t, b.Version, again.Version, "nothing changed")

	// cancelled (a tombstone): a confirm and a cancel change nothing
	tomb := ids.New().String()
	require.Equal(t, creditv3.BranchState_BRANCH_STATE_CANCELLED, cancel(tomb).State)
	b, err = confirm(tomb)
	require.NoError(t, err)
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_CANCELLED, b.State)
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_CANCELLED, cancel(tomb).State)

	// rejected: over the limit; a confirm and a cancel change nothing
	over := ids.New().String()
	require.Equal(t, creditv3.BranchState_BRANCH_STATE_REJECTED, fx.hold(over, fx.cust, "CNY", "100000").State)
	b, err = confirm(over)
	require.NoError(t, err)
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_REJECTED, b.State)
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_REJECTED, cancel(over).State)

	// expired by the participant's own job: a hold past its period; then confirm and cancel change nothing
	stale := ids.New().String()
	fx.hold(stale, fx.cust, "CNY", "10")
	_, err = fx.admin.Exec(ctx, "UPDATE "+itSchema+".branch SET hold_expires_at = now() - interval '1 second' WHERE id = $1", stale)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		q, err := fx.res.Query(ctx, &creditv3.QueryRequest{BranchId: stale})
		return err == nil && q.Branch.State == creditv3.BranchState_BRANCH_STATE_EXPIRED
	}, 30*time.Second, 200*time.Millisecond, "the expire-holds job expires it")
	b, err = confirm(stale)
	require.NoError(t, err)
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_EXPIRED, b.State)
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_EXPIRED, cancel(stale).State)

	// an adjustment (an order revision) is not in this version
	_, err = fx.res.Adjust(ctx, &creditv3.AdjustRequest{BranchId: stale})
	refused(err, codes.Unimplemented, "branch adjustment (order revisions) is not in this version of erp/credit")

	// the consumed 40 left the order exposure (the receivable side comes later); the rest came back
	assert.Equal(t, before, available(), "what was held came back or was consumed out of the order exposure")
}
