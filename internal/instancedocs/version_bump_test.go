package instancedocs

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type countingBump struct {
	calls int
	err   error
}

func (c *countingBump) bump(context.Context) error {
	c.calls++
	return c.err
}

// TestSetBumpsSettingsVersion: the ToS/privacy/homepage/custom-CSS bodies are
// cached in memory on every replica, so a write has to advance the shared
// counter or the other N-1 keep serving the previous legal text.
func TestSetBumpsSettingsVersion(t *testing.T) {
	ctx := context.Background()
	bumper := &countingBump{}
	svc := NewService(newFakeRepo(), WithVersionBump(bumper.bump))
	if err := svc.Load(ctx); err != nil {
		t.Fatalf("load: %v", err)
	}

	if _, err := svc.Set(ctx, NameHomepage, "# hello", uuid.Nil); err != nil {
		t.Fatalf("set: %v", err)
	}
	if bumper.calls != 1 {
		t.Fatalf("bump calls after Set = %d, want 1", bumper.calls)
	}

	// Clearing a document (empty body deletes the row) is just as visible to
	// the public delivery routes, so it bumps too.
	if _, err := svc.Set(ctx, NameHomepage, "", uuid.Nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if bumper.calls != 2 {
		t.Fatalf("bump calls after a clear = %d, want 2", bumper.calls)
	}
}

// TestSetNoBumpOnRejectedWrite: validation and unknown-name failures never
// touched the database, so they must not invalidate anybody's cache.
func TestSetNoBumpOnRejectedWrite(t *testing.T) {
	ctx := context.Background()
	bumper := &countingBump{}
	svc := NewService(newFakeRepo(), WithVersionBump(bumper.bump))

	if _, err := svc.Set(ctx, "nope", "body", uuid.Nil); !errors.Is(err, ErrUnknownName) {
		t.Fatalf("unknown name error = %v, want ErrUnknownName", err)
	}
	oversized := make([]byte, MaxBodyBytes(NameHomepage)+1)
	var verr *ValidationError
	if _, err := svc.Set(ctx, NameHomepage, string(oversized), uuid.Nil); !errors.As(err, &verr) {
		t.Fatalf("oversized body error = %v, want ValidationError", err)
	}
	if bumper.calls != 0 {
		t.Fatalf("bump calls for rejected writes = %d, want 0", bumper.calls)
	}
}

// TestSetBumpFailureSurfaces: see the instancesettings twin — a bump that fails
// means the fleet stays split, which is not something to swallow.
func TestSetBumpFailureSurfaces(t *testing.T) {
	sentinel := errors.New("counter unavailable")
	bumper := &countingBump{err: sentinel}
	svc := NewService(newFakeRepo(), WithVersionBump(bumper.bump))
	if _, err := svc.Set(context.Background(), NameCustomCSS, "body{}", uuid.Nil); !errors.Is(err, sentinel) {
		t.Fatalf("set error = %v, want the bump error", err)
	}
}
