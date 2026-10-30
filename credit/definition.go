// Package credit is the credit component: policies, limits by partner, legal entity and currency, and the credit
// reservation branches of protocol 04.
package credit

import (
	"embed"
	"io/fs"
	"time"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/component"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/brickKit/be-sdk-go/v3/runtime"
)

// Identity.
const (
	ID       = "erp/credit"
	Version  = "3.0.9"
	GRPCPort = 9088
)

// Dependencies (the IDs runtime.GRPC takes).
const (
	Partner  = "mdm/partner"
	Org      = "mdm/org"
	Currency = "mdm/currency"
	// Sales is the coordinator allowed to reserve (protocol 04).
	Sales = "erp/sales"
)

// The stream and its event types (contract/events).
const (
	Stream        = "erp.credit"
	PolicyChanged = "erp.credit_policy.changed.v1"
	LimitChanged  = "erp.credit_limit.changed.v1"
	LimitExceeded = "erp.credit_limit.exceeded.v1"
	BranchChanged = "erp.credit_branch.changed.v1"
)

// Resources and operations (contract/openapi.yaml names each route's operation).
const (
	Limits         = "erp.credit.limit"
	OpLimitRead    = "erp.credit.limit.read"
	OpLimitManage  = "erp.credit.limit.manage"
	OpLimitRequest = "erp.credit.limit.request"
	Policies       = "erp.credit.policy"
	OpPolicyRead   = "erp.credit.policy.read"
	OpPolicyManage = "erp.credit.policy.manage"
	System         = "erp.credit.system"
	OpSysRead      = "erp.credit.system.read"
	OpSysReserve   = "erp.credit.system.reserve"
)

// DimLegalEntity is the dimension of limits and policies (security 07).
const DimLegalEntity = "legal_entity"

// Parameters.
const HoldTTL = 5 * time.Minute // protocol 04's starting value for the hold period

//go:embed migrations/*.sql
var migrationFiles embed.FS

func migrations() fs.FS {
	sub, _ := fs.Sub(migrationFiles, "migrations")
	return sub
}

func fields(names ...string) map[string]sdkauthz.Field {
	out := map[string]sdkauthz.Field{}
	for _, n := range names {
		out[n] = sdkauthz.Field{Class: sdkauthz.Ordinary}
	}
	return out
}

// Definitions are the component's resources and operations; limits and policies carry the legal entity.
func Definitions() *sdkauthz.Definitions {
	le := map[string]sdkauthz.Dimension{DimLegalEntity: {Kind: sdkauthz.ValueSet, Column: "legal_entity_id"}}
	return &sdkauthz.Definitions{Component: ID,
		Resources: []sdkauthz.Resource{
			{Type: Limits, ViewOperation: OpLimitRead, Dimensions: le,
				Fields: fields("id", "partner_id", "legal_entity_id", "currency", "limit", "state", "order_exposure",
					"receivable_exposure", "used", "available", "receivables_tracked", "version", "updated_at")},
			{Type: Policies, ViewOperation: OpPolicyRead, Dimensions: le,
				Fields: fields("id", "legal_entity_id", "without_limit", "version", "updated_at")},
			{Type: System, ViewOperation: OpSysRead, Dimensions: map[string]sdkauthz.Dimension{},
				Fields: map[string]sdkauthz.Field{}},
		},
		Operations: []sdkauthz.Operation{
			{Key: OpLimitRead, ResourceType: Limits, Purpose: "read", RiskClass: "low"},
			{Key: OpLimitManage, ResourceType: Limits, Purpose: "manage", RiskClass: "high", SideEffects: []string{"business_write"}, Audit: true},
			{Key: OpLimitRequest, ResourceType: Limits, Purpose: "action", RiskClass: "low", SideEffects: []string{"business_write"}, Audit: true},
			{Key: OpPolicyRead, ResourceType: Policies, Purpose: "read", RiskClass: "low"},
			{Key: OpPolicyManage, ResourceType: Policies, Purpose: "manage", RiskClass: "high", SideEffects: []string{"business_write"}, Audit: true},
			{Key: OpSysRead, ResourceType: System, Purpose: "read", RiskClass: "low"},
			{Key: OpSysReserve, ResourceType: System, Purpose: "action", RiskClass: "low", SideEffects: []string{"business_write"}},
		}}
}

// Definition is the component as the launcher and the shells take it.
func Definition() component.Definition {
	return component.Definition{ID: ID, Version: Version, Migrations: migrations(), GRPCPort: GRPCPort,
		Streams: []events.Stream{{Name: Stream, Partitions: 4}}, Authz: Definitions(),
		New: func(rt *runtime.Runtime) (component.Module, error) { return New(rt) }}
}
