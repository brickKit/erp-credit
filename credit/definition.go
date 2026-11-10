// Package credit is the credit component: policies, limits by partner, legal entity and currency, and the credit
// reservation branches of protocol 04.
package credit

import (
	"embed"
	"io/fs"

	core "github.com/brickKit/erp-credit/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/component"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/brickKit/be-sdk-go/v3/runtime"
)

// Identity.
const (
	Version  = "3.0.36"
	GRPCPort = 9088
)

// protocol 04's starting value for the hold period

//go:embed migrations/*.sql
var migrationFiles embed.FS

func migrations() fs.FS {
	sub, _ := fs.Sub(migrationFiles, "migrations")
	return sub
}

// Definition is the component as the launcher and the shells take it.
func Definition() component.Definition {
	return component.Definition{ID: core.ID, Version: Version, Migrations: migrations(), GRPCPort: GRPCPort,
		Streams: []events.Stream{{Name: core.Stream, Partitions: 4}}, Authz: core.Definitions(), Resend: core.OpResend, Recovery: core.OpRecover,
		New: func(rt *runtime.Runtime) (component.Module, error) { return New(rt) }}
}
