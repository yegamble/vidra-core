//go:build debug

package debug

import (
	"github.com/go-chi/chi/v5"
)

// In debug builds we set Enabled=true and (later, when the worker
// dependency is wired) replace RegisterDebugRoutes with a function
// that mounts the actual endpoints. routes.go's call site is
// unchanged; only the variables flip.
//
// The worker-tick handler itself lives in balance_worker_debug.go (also
// debug-tagged). It registers itself via SetWorkerTickHandler from main.go.
func init() {
	Enabled = true
	RegisterDebugRoutes = func(r chi.Router) {
		if workerTickHandler != nil {
			r.Post("/debug/balance-worker/tick", workerTickHandler)
		}
	}
}
