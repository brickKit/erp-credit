//go:build integration

package itest

// The credit component against PostgreSQL and the bus over its real surfaces (L2). mdm/org, mdm/partner and
// mdm/currency are stand-ins that speak their published contracts over mTLS; the coordinator is the test, calling as
// erp/sales. Needs BE_SDK_TEST_PG_ADMIN and BE_SDK_TEST_NATS_URL. The member listens on 28088 (user) and 9088
// (system); the stand-ins on 19099 (mdm/org), 19100 (mdm/partner) and 19101 (mdm/currency).
// Run: go test -tags integration ./itest/ (make test-integration); the index of the steps is itest/AGENTS.md

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

	credit "github.com/brickKit/erp-credit/v3/credit"

	core "github.com/brickKit/erp-credit/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/config"
	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/launcher"
	"github.com/brickKit/be-sdk-go/v3/sectest"
	creditv3 "github.com/brickKit/erp-credit/contract/v3/gen/go/be/erp/credit/v3"
	currencyv3 "github.com/brickKit/mdm-currency/contract/v3/gen/go/be/mdm/currency/v3"
	orgv3 "github.com/brickKit/mdm-org/contract/v3/gen/go/be/mdm/org/v3"
	partnerv3 "github.com/brickKit/mdm-partner/contract/v3/gen/go/be/mdm/partner/v3"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
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

// fixture is the running member, its stand-ins and what one step leaves for the next.
type fixture struct {
	t                        *testing.T
	ctx                      context.Context
	admin                    *pgx.Conn
	w                        *sectest.World
	alice                    client
	conn                     *grpc.ClientConn // the system surface, as erp/sales
	res                      creditv3.ReservationServiceClient
	le, cust, cash, stranger string         // the legal entity; customers with a limit, without one; not a customer
	lim                      map[string]any // the customer's CNY limit (limits)
}

func TestCreditEndToEnd(t *testing.T) {
	fx := start(t)
	fx.policies()
	fx.limits()
	fx.racingHolds()
	fx.branchLifecycle()
	fx.lateAndExpired()
	fx.participant()
	fx.suspendedLimit()
	fx.recoveryChecks()
}

// hold holds amt of cur for the partner in the legal entity on a new or given branch.
func (fx *fixture) hold(branch, partner, cur, amt string) *creditv3.Branch {
	fx.t.Helper()
	h, err := fx.res.Hold(fx.ctx, &creditv3.HoldRequest{BranchId: branch, PartnerId: partner, LegalEntityId: fx.le, Currency: cur,
		Amount: amt, Reference: &creditv3.Reference{Kind: "sales_order", Id: "SO-" + branch[:8], Revision: 1}})
	require.NoError(fx.t, err)
	return h.Branch
}

// start makes the world and the stand-ins, migrates and runs the member.
func start(t *testing.T) *fixture {
	adminURL, natsURL := os.Getenv("BE_SDK_TEST_PG_ADMIN"), os.Getenv("BE_SDK_TEST_NATS_URL")
	if adminURL == "" || natsURL == "" {
		t.Fatal("BE_SDK_TEST_PG_ADMIN and BE_SDK_TEST_NATS_URL are needed")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, adminURL)
	require.NoError(t, err)
	t.Cleanup(func() { admin.Close(ctx) })
	_, err = admin.Exec(ctx, "DROP SCHEMA IF EXISTS "+itSchema+" CASCADE; CREATE SCHEMA "+itSchema)
	require.NoError(t, err)
	u, err := url.Parse(adminURL)
	require.NoError(t, err)
	pw, _ := u.User.Password()
	w := sectest.New(t)
	w.GrantAll(sectest.Alice, core.Definitions())

	le, cust, other, cash, stranger := ids.New().String(), ids.New().String(), ids.New().String(), ids.New().String(), ids.New().String()
	portFree(t, itPort, itSystem, itOrg, itPartner, itCurrency)
	serve(t, itOrg, w, core.Org, func(s *grpc.Server) { orgv3.RegisterSourceServiceServer(s, &org{known: map[string]bool{le: true}}) })
	serve(t, itPartner, w, core.Partner, func(s *grpc.Server) {
		partnerv3.RegisterPartnerServiceServer(s, &partners{customers: map[string]bool{cust: true, other: true, cash: true}})
	})
	serve(t, itCurrency, w, core.Currency, func(s *grpc.Server) { currencyv3.RegisterSourceServiceServer(s, currencies{}) })

	env := w.Env(t, core.ID)
	for k, v := range map[string]string{"COMPONENT_ID": core.ID, "COMPONENT_VERSION": credit.Version, "PORT": itPort,
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
	require.Equal(t, launcher.ExitOK, launcher.Run(ctx, credit.Definition(), migrate))
	sctx, stop := context.WithCancel(ctx)
	done := make(chan int, 1)
	go func() { done <- launcher.Run(sctx, credit.Definition(), opts) }()
	t.Cleanup(func() { stop(); <-done })
	require.Eventually(t, func() bool {
		r, err := http.Get(itBase + "/readyz")
		if err == nil {
			r.Body.Close()
		}
		return err == nil && r.StatusCode == 200
	}, 20*time.Second, 100*time.Millisecond)

	conn, err := grpc.NewClient(itSystem, grpc.WithTransportCredentials(credentials.NewTLS(w.TLS(t, core.Sales).ClientConfig(core.ID))))
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	return &fixture{t: t, ctx: ctx, admin: admin, w: w, alice: client{t, w.Bearer(sectest.Alice)}, conn: conn,
		res: creditv3.NewReservationServiceClient(conn), le: le, cust: cust, cash: cash, stranger: stranger}
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
