// Package tenant holds the tenants of the hub and their git repository (concept-multi-tenancy).
// Proposed: no code yet. The blank imports state the required interfaces of bb-tenants until the
// first code uses them.
package tenant

import (
	_ "github.com/mrsimpson/agentic-office/hub/archive/storage"
	_ "github.com/mrsimpson/agentic-office/hub/identity/tokencheck"
)
