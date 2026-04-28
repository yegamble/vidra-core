package inner_circle

import (
	"os"
	"strings"
	"testing"
)

// TestExpiryJob_RegisteredInBootstrapper guards against accidental removal of
// the goroutine that runs the membership-expiry sweep. If a future refactor
// drops `go job.Run(ctx)`, expirations stop and nobody notices until a
// paying user complains months later. Cheap text-grep test, high regression
// value — see Phase 9 plan T3 DoD.
func TestExpiryJob_RegisteredInBootstrapper(t *testing.T) {
	candidates := []string{
		"../../../internal/app/app.go",
		"../../app/app.go",
	}
	var content string
	for _, p := range candidates {
		b, err := os.ReadFile(p)
		if err == nil {
			content = string(b)
			break
		}
	}
	if content == "" {
		t.Skip("could not locate app.go from this test working directory")
	}

	// Two assertions: the job is constructed, and a goroutine runs it.
	if !strings.Contains(content, "icusecase.NewExpiryJob(") {
		t.Fatalf("expected app.go to construct the Inner Circle expiry job via icusecase.NewExpiryJob")
	}
	if !strings.Contains(content, "job.Run(ctx)") && !strings.Contains(content, "ExpiryJob).Run(ctx)") {
		t.Fatalf("expected app.go to spawn `go ... job.Run(ctx)` for the expiry sweep")
	}
}
