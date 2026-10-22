package credit

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/launcher"
	"github.com/brickKit/be-sdk-go/v3/mergesafety"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheManifestDeclaresWhatTheSDKReads: component.yaml's configSchema is exactly the keys the SDK reads.
func TestTheManifestDeclaresWhatTheSDKReads(t *testing.T) {
	raw, err := os.ReadFile("../component.yaml")
	require.NoError(t, err)
	if p := launcher.ManifestProblems(raw, Definition(), nil); len(p) > 0 {
		t.Errorf("component.yaml's configSchema differs from what the SDK reads:\n%v\nthe block:\n%s", p,
			launcher.ConfigSchemaYAML(Definition(), nil))
	}
}

// TestTheManifestListsThePublishedEvents: component.yaml's events.publishes is exactly the event contracts.
func TestTheManifestListsThePublishedEvents(t *testing.T) {
	raw, err := os.ReadFile("../component.yaml")
	require.NoError(t, err)
	assert.Empty(t, launcher.EventProblems(raw, os.DirFS("../contract/events")))
}

// TestTheRoutesAreTheOpenAPIDocument: every route is in contract/openapi.yaml with its operation, and nothing else is.
func TestTheRoutesAreTheOpenAPIDocument(t *testing.T) {
	raw, err := os.ReadFile("../contract/openapi.yaml")
	require.NoError(t, err)
	var r httpx.Routes
	require.NoError(t, (&Module{}).Routes(&r))
	assert.Empty(t, httpx.OpenAPIProblems(raw, r.List()))
}

// TestThePayloadsFitTheirSchemas: what events and reads carry is what contract/events promises.
func TestThePayloadsFitTheirSchemas(t *testing.T) {
	le, pa, br := "01a15ab0-0000-7000-8000-0000000000a1", "01a15ab0-0000-7000-8000-0000000000b1", "01a15ab0-0000-7000-8000-0000000000c1"
	at, amt, cur, lim, avail, rev := "2026-10-20T08:00:00.000000Z", "1200.5", "CNY", "5000", "-200", int64(1)
	reason := "over_limit"
	for typ, v := range map[string]any{
		PolicyChanged: Policy{ID: le, LegalEntityID: le, WithoutLimit: "block", Version: 1, UpdatedAt: at},
		LimitChanged: Limit{ID: br, PartnerID: pa, LegalEntityID: le, Currency: cur, Limit: &lim, State: "active",
			OrderExposure: "5200", ReceivableExposure: "0", Used: "5200", Available: &avail, Version: 4, UpdatedAt: at},
		BranchChanged: Branch{ID: br, PartnerID: &pa, LegalEntityID: &le, Currency: &cur, Amount: &amt, Consumed: &amt,
			Reference: &Reference{Kind: "sales_order", ID: "SO-1", Revision: &rev}, State: "rejected", Reason: &reason,
			Version: 1, UpdatedAt: at},
		LimitExceeded: map[string]any{"fact_id": br, "partner_id": pa, "legal_entity_id": le, "currency": cur,
			"requested": amt, "available": "300", "reference": nil, "occurred_at": at},
	} {
		c := jsonschema.NewCompiler()
		s, err := c.Compile("../contract/events/" + typ + ".schema.json")
		require.NoError(t, err, typ)
		b, err := json.Marshal(v)
		require.NoError(t, err)
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
		require.NoError(t, err)
		assert.NoError(t, s.Validate(doc), typ)
	}
	// a customer outside credit control, and a tombstone
	for typ, v := range map[string]any{
		LimitChanged:  Limit{ID: br, PartnerID: pa, LegalEntityID: le, Currency: cur, State: "active", OrderExposure: "10", ReceivableExposure: "0", Used: "10", Version: 1, UpdatedAt: at},
		BranchChanged: Branch{ID: br, State: "cancelled", Version: 1, UpdatedAt: at},
	} {
		c := jsonschema.NewCompiler()
		s, err := c.Compile("../contract/events/" + typ + ".schema.json")
		require.NoError(t, err)
		b, _ := json.Marshal(v)
		doc, _ := jsonschema.UnmarshalJSON(bytes.NewReader(b))
		assert.NoError(t, s.Validate(doc), typ)
	}
}

// TestTheModuleIsMergeSafe: the module is compiled into the Go core shell.
func TestTheModuleIsMergeSafe(t *testing.T) {
	findings, err := mergesafety.CheckDir(".")
	require.NoError(t, err)
	for _, f := range findings {
		t.Errorf("%v", f)
	}
}
