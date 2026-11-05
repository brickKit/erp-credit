package credit

import (
	"github.com/brickKit/be-sdk-go/v3/recovery"
)

// RecoveryChecks registers what a full recovery asks this member (be-protocol chapter 14): whether the objects this
// member audits are still here, at which version, and the checks it reports.
func (m *Module) RecoveryChecks(c *recovery.Checks) error {
	c.Presence(Limits, recovery.TablePresence("credit_limit", "id", "version"))
	c.Presence(Policies, recovery.TablePresence("credit_policy", "legal_entity_id", "version")) // a policy's id is its legal entity
	return nil
}
