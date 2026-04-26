// Package debug exposes debug-only HTTP endpoints (e.g., the
// balance-worker tick used by Phase 8B Task 13 E2E TS-009). The package
// always compiles; the actual handlers are gated behind the `debug`
// build tag (see debug_enabled.go). Production binaries built without
// `-tags=debug` get the no-op stub from this file.
package debug

import "github.com/go-chi/chi/v5"

// Enabled is set to true at init() time by debug_enabled.go (only
// compiled in `-tags=debug` builds). cmd/server/main.go reads this to
// fatal when ENV=production AND Enabled=true (defense in depth — build
// tag is the primary safeguard, runtime check is the canary).
var Enabled = false

// RegisterDebugRoutes mounts debug handlers onto the given chi router.
// In non-debug builds this is a no-op. In debug builds it is replaced
// (via init() in debug_enabled.go) with a real implementation that
// mounts the worker-tick endpoint.
//
// routes.go always calls this — non-debug builds get the no-op,
// debug builds get the real handlers. No symbol-resolution surprises.
var RegisterDebugRoutes func(r chi.Router) = func(_ chi.Router) {}
