//go:build integration

package credit

// The credit component against PostgreSQL and the bus over its real surfaces (L2). mdm/org, mdm/partner and
// mdm/currency are stand-ins that speak their published contracts over mTLS; the coordinator is the test, calling as
// erp/sales. Needs BE_SDK_TEST_PG_ADMIN and BE_SDK_TEST_NATS_URL. The member listens on 28088 (user) and 9088
// (system); the stand-ins on 19099 (mdm/org), 19100 (mdm/partner) and 19101 (mdm/currency).
// Run: go test -tags integration ./credit/

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brickKit/be-sdk-go/v3/config"
	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/launcher"
	"github.com/brickKit/be-sdk-go/v3/sectest"
	creditv3 "github.com/brickKit/erp-credit/contract/v3/gen/go/be/erp/credit/v3"
	currencyv3 "github.com/brickKit/mdm-currency/contract/v3/gen/go/be/mdm/currency/v3"
	orgv3 "github.com/brickKit/mdm-org/contract/v3/gen/go/be/mdm/org/v3"
	partnerv3 "github.com/brickKit/mdm-partner/contract/v3/gen/go/be/mdm/partner/v3"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

const (
	itPort     = "28088"
	itBase     = "http://localhost:" + itPort
	itSystem   = "localhost:9088"
	itOrg      = "localhost:19099"
	itPartner  = "localhost:19100"
	itCurrency = "localhost:19101"
	itSchema   = "erp_credit_it"
)

// org is the stand-in mdm/org: every legal entity it knows is active.
type org struct {
	orgv3.UnimplementedSourceServiceServer
	known map[string]bool
}

func (o *org) Read(_ context.Context, in *orgv3.ReadRequest) (*orgv3.ReadResponse, error) {
	out := &orgv3.ReadResponse{Stamp: &orgv3.Stamp{}}
	for _, id := range in.GetIds() {
		r := &orgv3.ReadResult{Id: id, State: orgv3.ReadState_READ_STATE_PURGED}
		if o.known[id] {
			data, _ := json.Marshal(map[string]any{"id": id, "state": "active"})
			r = &orgv3.ReadResult{Id: id, State: orgv3.ReadState_READ_STATE_EXISTS, Version: 1, Data: data}
		}
		out.Results = append(out.Results, r)
	}
	return out, nil
}

// partners is the stand-in mdm/partner: the customers it knows are eligible to sell to in any legal entity.
type partners struct {
	partnerv3.UnimplementedPartnerServiceServer
	customers map[string]bool
}

func (p *partners) CheckEligibility(_ context.Context, in *partnerv3.CheckEligibilityRequest) (*partnerv3.CheckEligibilityResponse, error) {
	if p.customers[in.GetPartnerId()] {
		return &partnerv3.CheckEligibilityResponse{Eligible: true, Reason: "eligible", EvaluatedAt: time.Now().UTC().Format(time.RFC3339)}, nil
	}
	return &partnerv3.CheckEligibilityResponse{Reason: "role_not_opened", EvaluatedAt: time.Now().UTC().Format(time.RFC3339)}, nil
}

// currencies is the stand-in mdm/currency: CNY (2 places) and JPY (0) are active, ITL is not.
type currencies struct {
	currencyv3.UnimplementedSourceServiceServer
}

func (currencies) Read(_ context.Context, in *currencyv3.ReadRequest) (*currencyv3.ReadResponse, error) {
	known := map[string]map[string]any{"CNY": {"code": "CNY", "minor_unit": 2, "active": true},
		"JPY": {"code": "JPY", "minor_unit": 0, "active": true}, "ITL": {"code": "ITL", "minor_unit": 0, "active": false}}
	out := &currencyv3.ReadResponse{Stamp: &currencyv3.Stamp{}}
	for _, id := range in.GetIds() {
		r := &currencyv3.ReadResult{Id: id, State: currencyv3.ReadState_READ_STATE_PURGED}
		if v, ok := known[id]; ok {
			data, _ := json.Marshal(v)
			r = &currencyv3.ReadResult{Id: id, State: currencyv3.ReadState_READ_STATE_EXISTS, Version: 1, Data: data}
		}
		out.Results = append(out.Results, r)
	}
	return out, nil
}

func TestCreditEndToEnd(t *testing.T) {
	adminURL, natsURL := os.Getenv("BE_SDK_TEST_PG_ADMIN"), os.Getenv("BE_SDK_TEST_NATS_URL")
	if adminURL == "" || natsURL == "" {
		t.Fatal("BE_SDK_TEST_PG_ADMIN and BE_SDK_TEST_NATS_URL are needed")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, adminURL)
	require.NoError(t, err)
	defer admin.Close(ctx)
	_, err = admin.Exec(ctx, "DROP SCHEMA IF EXISTS "+itSchema+" CASCADE; CREATE SCHEMA "+itSchema)
	require.NoError(t, err)
	u, err := url.Parse(adminURL)
	require.NoError(t, err)
	pw, _ := u.User.Password()
	w := sectest.New(t)
	w.GrantAll(sectest.Alice, Definitions())

	le, cust, other, cash, stranger := ids.New().String(), ids.New().String(), ids.New().String(), ids.New().String(), ids.New().String()
	portFree(t, itPort, itSystem, itOrg, itPartner, itCurrency)
	serve(t, itOrg, w, Org, func(s *grpc.Server) { orgv3.RegisterSourceServiceServer(s, &org{known: map[string]bool{le: true}}) })
	serve(t, itPartner, w, Partner, func(s *grpc.Server) {
		partnerv3.RegisterPartnerServiceServer(s, &partners{customers: map[string]bool{cust: true, other: true, cash: true}})
	})
	serve(t, itCurrency, w, Currency, func(s *grpc.Server) { currencyv3.RegisterSourceServiceServer(s, currencies{}) })

	env := w.Env(t, ID)
	for k, v := range map[string]string{"COMPONENT_ID": ID, "COMPONENT_VERSION": Version, "PORT": itPort,
		"STOP_DRAIN_DELAY": "50ms", "PG_HOST": u.Hostname(), "PG_PORT": u.Port(), "PG_DATABASE": strings.TrimPrefix(u.Path, "/"),
		"PG_USER": u.User.Username(), "PG_PASSWORD": pw, "PG_SCHEMA": itSchema, "PG_OWNER_USER": u.User.Username(),
		"PG_OWNER_PASSWORD": pw, "BUS_URL": natsURL, "IMPLEMENTATION_INSTANCE": ids.New().String(),
		"MDM_ORG_GRPC_ENDPOINT": "http://" + itOrg, "MDM_PARTNER_GRPC_ENDPOINT": "http://" + itPartner,
		"MDM_CURRENCY_GRPC_ENDPOINT": "http://" + itCurrency} {
		env[k] = v
	}
	var logs bytes.Buffer
	var logMu sync.Mutex
	t.Cleanup(func() {
		if t.Failed() {
			logMu.Lock()
			t.Log(logs.String())
			logMu.Unlock()
		}
	})
	out := &lockedWriter{w: &logs, mu: &logMu}
	opts := launcher.Options{Stdout: out, Stderr: out,
		Env: config.Env{LookupEnv: func(k string) (string, bool) { v, ok := env[k]; return v, ok }, Read: os.ReadFile}}
	migrate := opts
	migrate.Args = []string{"migrate"}
	require.Equal(t, launcher.ExitOK, launcher.Run(ctx, Definition(), migrate))
	sctx, stop := context.WithCancel(ctx)
	done := make(chan int, 1)
	go func() { done <- launcher.Run(sctx, Definition(), opts) }()
	defer func() { stop(); <-done }()
	require.Eventually(t, func() bool {
		r, err := http.Get(itBase + "/readyz")
		if err == nil {
			r.Body.Close()
		}
		return err == nil && r.StatusCode == 200
	}, 20*time.Second, 100*time.Millisecond)
	alice := client{t, w.Bearer(sectest.Alice)}

	conn, err := grpc.NewClient(itSystem, grpc.WithTransportCredentials(credentials.NewTLS(w.TLS(t, Sales).ClientConfig(ID))))
	require.NoError(t, err)
	defer conn.Close()
	res := creditv3.NewReservationServiceClient(conn)
	hold := func(branch, partner, cur, amt string) *creditv3.Branch {
		t.Helper()
		h, err := res.Hold(ctx, &creditv3.HoldRequest{BranchId: branch, PartnerId: partner, LegalEntityId: le, Currency: cur,
			Amount: amt, Reference: &creditv3.Reference{Kind: "sales_order", Id: "SO-" + branch[:8], Revision: 1}})
		require.NoError(t, err)
		return h.Branch
	}

	// a legal entity without a policy blocks a customer without a limit
	b := hold(ids.New().String(), cash, "CNY", "100")
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_REJECTED, b.State)
	assert.Equal(t, "no_limit", b.Reason)
	code, pol := alice.do("PUT", "/api/credit/policies/"+le, map[string]any{"without_limit": "unlimited"})
	require.Equal(t, 200, code, pol)
	// unlimited: the customer is held and counted, never refused, and a limit row without an amount appears
	b = hold(ids.New().String(), cash, "CNY", "99999999")
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_HELD, b.State)
	_, lims := alice.do("GET", "/api/credit/limits?partner_id="+cash, nil)
	cashLimit := lims["limits"].([]any)[0].(map[string]any)
	assert.Nil(t, cashLimit["limit"])
	assert.Equal(t, "99999999", cashLimit["order_exposure"])

	// limits: the partner must be a customer of the legal entity, the currency active, the amount in its minor unit
	code, bad := alice.do("POST", "/api/credit/limits", map[string]any{"partner_id": stranger, "legal_entity_id": le, "currency": "CNY", "limit": "1000"})
	assert.Equal(t, 409, code)
	assert.Equal(t, "CUSTOMER_NOT_ELIGIBLE", bad["reason"])
	code, bad = alice.do("POST", "/api/credit/limits", map[string]any{"partner_id": cust, "legal_entity_id": le, "currency": "ITL", "limit": "1000"})
	assert.Equal(t, 409, code)
	assert.Equal(t, "CURRENCY_NOT_USABLE", bad["reason"])
	code, _ = alice.do("POST", "/api/credit/limits", map[string]any{"partner_id": cust, "legal_entity_id": le, "currency": "JPY", "limit": "1000.5"})
	assert.Equal(t, 400, code, "yen has no minor unit")
	code, lim := alice.do("POST", "/api/credit/limits", map[string]any{"partner_id": cust, "legal_entity_id": le, "currency": "CNY", "limit": "1000.00"})
	require.Equal(t, 200, code, lim)
	assert.Equal(t, "1000", lim["limit"])
	code, _ = alice.do("POST", "/api/credit/limits", map[string]any{"partner_id": cust, "legal_entity_id": le, "currency": "CNY", "limit": "1"})
	assert.Equal(t, 409, code, "one limit a customer, legal entity and currency")

	// the boundary card's story: two orders confirm at once, only the one within the limit succeeds
	var wg sync.WaitGroup
	var mu sync.Mutex
	got := map[creditv3.BranchState]int{}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := hold(ids.New().String(), cust, "CNY", "600")
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
			b := hold(ids.New().String(), cust, "CNY", "50")
			mu.Lock()
			got[b.State]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	assert.Equal(t, 8, got[creditv3.BranchState_BRANCH_STATE_HELD], got)
	_, l := alice.do("GET", "/api/credit/limits/"+lim["id"].(string), nil)
	assert.Equal(t, "0", l["available"])
	var exceeded int
	require.NoError(t, admin.QueryRow(ctx, "SELECT count(*) FROM "+itSchema+".be_outbox WHERE type = $1", LimitExceeded).Scan(&exceeded))
	assert.Equal(t, 3, exceeded, "each refusal over the limit is a fact for the credit manager")
	// the limit is per currency: no conversion, so the customer has no USD limit and the policy decides
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_HELD, hold(ids.New().String(), cust, "JPY", "5").State, "unlimited policy")

	// raise the limit; a confirmed branch never expires, a consume takes off it, a cancel gives back the rest
	code, l = alice.do("PATCH", "/api/credit/limits/"+lim["id"].(string), map[string]any{"limit": "2000"})
	require.Equal(t, 200, code, l)
	order := ids.New().String()
	require.Equal(t, creditv3.BranchState_BRANCH_STATE_HELD, hold(order, cust, "CNY", "300").State)
	again := hold(order, cust, "CNY", "300")
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_HELD, again.State, "the same hold again: the first answer")
	_, err = res.Hold(ctx, &creditv3.HoldRequest{BranchId: order, PartnerId: cust, LegalEntityId: le, Currency: "CNY", Amount: "301",
		Reference: &creditv3.Reference{Kind: "sales_order", Id: "SO-" + order[:8], Revision: 1}})
	assert.Equal(t, codes.AlreadyExists, status.Code(err), "RS17")
	_, l = alice.do("GET", "/api/credit/limits/"+lim["id"].(string), nil)
	assert.Equal(t, "700", l["available"], "held once")
	cf, err := res.Confirm(ctx, &creditv3.ConfirmRequest{BranchId: order})
	require.NoError(t, err)
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_COMMITTED, cf.Branch.State)
	cs, err := res.Consume(ctx, &creditv3.ConsumeRequest{ConsumptionId: "INV-1", BranchId: order, Amount: "100"})
	require.NoError(t, err)
	assert.Equal(t, "100", cs.Branch.Consumed)
	_, err = res.Consume(ctx, &creditv3.ConsumeRequest{ConsumptionId: "INV-1", BranchId: order, Amount: "100"})
	require.NoError(t, err, "the same consume again is harmless")
	cc, err := res.Cancel(ctx, &creditv3.CancelRequest{BranchId: order})
	require.NoError(t, err)
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_RELEASED, cc.Branch.State)
	_, l = alice.do("GET", "/api/credit/limits/"+lim["id"].(string), nil)
	assert.Equal(t, "1000", l["available"], "the consumed 100 left the order exposure, the other 200 came back")

	// RS04: a cancel before the hold leaves a tombstone; the late hold is refused
	late := ids.New().String()
	_, err = res.Cancel(ctx, &creditv3.CancelRequest{BranchId: late})
	require.NoError(t, err)
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_CANCELLED, hold(late, cust, "CNY", "10").State)
	q, err := res.Query(ctx, &creditv3.QueryRequest{BranchId: ids.New().String()})
	require.NoError(t, err)
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_ABSENT, q.Branch.State)

	// an expired hold is not revived
	stale := ids.New().String()
	hold(stale, cust, "CNY", "10")
	_, err = admin.Exec(ctx, "UPDATE "+itSchema+".branch SET hold_expires_at = now() - interval '1 second' WHERE id = $1", stale)
	require.NoError(t, err)
	cf, err = res.Confirm(ctx, &creditv3.ConfirmRequest{BranchId: stale})
	require.NoError(t, err)
	assert.Equal(t, creditv3.BranchState_BRANCH_STATE_EXPIRED, cf.Branch.State)

	// a suspended limit takes no new hold; its committed branches go on
	_, _ = alice.do("PATCH", "/api/credit/limits/"+lim["id"].(string), map[string]any{"state": "suspended"})
	b = hold(ids.New().String(), cust, "CNY", "1")
	assert.Equal(t, "limit_suspended", b.Reason)

	av, err := creditv3.NewCreditServiceClient(conn).Available(ctx, &creditv3.AvailableRequest{PartnerId: cust, LegalEntityId: le, Currency: "CNY"})
	require.NoError(t, err)
	assert.True(t, av.HasLimit)
	assert.Equal(t, "2000", av.Limit)
	assert.Equal(t, "suspended", av.State)
	assert.False(t, av.ReceivablesTracked)
}

type client struct {
	t     *testing.T
	token string
}

func (c client) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, itBase+path, r)
	req.Header.Set("Authorization", c.token)
	req.Header.Set("Content-Type", "application/json")
	if method == "POST" {
		req.Header.Set("Idempotency-Key", ids.New().String())
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(c.t, err)
	defer resp.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func serve(t *testing.T, addr string, w *sectest.World, as string, register func(*grpc.Server)) {
	ln, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(w.TLS(t, as).ServerConfig())))
	register(srv)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)
}

type lockedWriter struct {
	w  io.Writer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// portFree fails at once when a port the member needs is taken: a running cell or another member would answer in
// its place, and the test would assert against another process.
func portFree(t *testing.T, addrs ...string) {
	for _, a := range addrs {
		if !strings.Contains(a, ":") {
			a = "localhost:" + a
		}
		ln, err := net.Listen("tcp", a)
		require.NoError(t, err, "port %s is taken: is a cell or another member running?", a)
		_ = ln.Close()
	}
}
