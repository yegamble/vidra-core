package admin

import (
	"encoding/json"
	"net/http"
	"runtime"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
)

var startTime = time.Now()

// DebugHandlers handles server debug endpoints.
type DebugHandlers struct{}

// NewDebugHandlers returns a new DebugHandlers.
func NewDebugHandlers() *DebugHandlers {
	return &DebugHandlers{}
}

// GetDebugInfo handles GET /api/v1/server/debug.
// Returns system information for admins.
func (h *DebugHandlers) GetDebugInfo(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.UserRoleKey).(string)
	if role != "admin" {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Admin access required"))
		return
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	info := map[string]interface{}{
		"goVersion":     runtime.Version(),
		"numGoroutine":  runtime.NumGoroutine(),
		"numCPU":        runtime.NumCPU(),
		"memAlloc":      mem.Alloc,
		"memTotalAlloc": mem.TotalAlloc,
		"memSys":        mem.Sys,
		"memNumGC":      mem.NumGC,
		"uptime":        time.Since(startTime).String(),
		"os":            runtime.GOOS,
		"arch":          runtime.GOARCH,
	}

	shared.WriteJSON(w, http.StatusOK, info)
}

type runCommandRequest struct {
	Command string `json:"command"`
}

// RunCommand handles POST /api/v1/server/debug/run-command.
// Executes a limited set of admin debug commands.
func (h *DebugHandlers) RunCommand(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.UserRoleKey).(string)
	if role != "admin" {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Admin access required"))
		return
	}

	var req runCommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid request body"))
		return
	}

	if req.Command == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Command is required"))
		return
	}

	// Only allow safe debug commands
	allowedCommands := map[string]func() interface{}{
		"gc": func() interface{} {
			runtime.GC()
			return map[string]string{"result": "garbage collection triggered"}
		},
		"goroutines": func() interface{} {
			return map[string]int{"goroutines": runtime.NumGoroutine()}
		},
		"memstats": func() interface{} {
			var mem runtime.MemStats
			runtime.ReadMemStats(&mem)
			return map[string]uint64{
				"alloc":      mem.Alloc,
				"totalAlloc": mem.TotalAlloc,
				"sys":        mem.Sys,
				"numGC":      uint64(mem.NumGC),
			}
		},
	}

	fn, ok := allowedCommands[req.Command]
	if !ok {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Unknown command: "+req.Command))
		return
	}

	shared.WriteJSON(w, http.StatusOK, fn())
}
