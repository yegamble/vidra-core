package notification

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestStaffQueueHookIsolatesErrorsAndPanics(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	id := uuid.New()

	StaffQueueHook(logger, "q", func(context.Context, uuid.UUID) (int64, error) { panic("boom") })(context.Background(), id)
	if !strings.Contains(buf.String(), "panicked") {
		t.Errorf("panic not recovered+logged: %q", buf.String())
	}
	buf.Reset()
	StaffQueueHook(logger, "q", func(context.Context, uuid.UUID) (int64, error) { return 0, errors.New("db down") })(context.Background(), id)
	if !strings.Contains(buf.String(), "failed") {
		t.Errorf("error not logged: %q", buf.String())
	}
	StaffQueueHook(logger, "q", nil)(context.Background(), id) // must not panic
}
