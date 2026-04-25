package payments

import (
	"context"
	"encoding/json"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/port"

	"github.com/google/uuid"
)

// RepoNotificationEmitter adapts a port.NotificationRepository to the
// NotificationEmitter interface used by LedgerService / PayoutService.
// Intended for wiring in app.go — callers outside test code should use this.
type RepoNotificationEmitter struct {
	repo port.NotificationRepository
}

// NewRepoNotificationEmitter constructs an emitter backed by the notifications repo.
func NewRepoNotificationEmitter(repo port.NotificationRepository) *RepoNotificationEmitter {
	return &RepoNotificationEmitter{repo: repo}
}

// Emit persists a notification via the underlying repository.
// Errors bubble up to the caller (LedgerService logs + ignores; PayoutService
// treats admin-notify failures as non-fatal).
func (e *RepoNotificationEmitter) Emit(ctx context.Context, userID string, nType domain.NotificationType, title, message string, data map[string]interface{}) error {
	if e == nil || e.repo == nil {
		return nil
	}
	uID, err := uuid.Parse(userID)
	if err != nil {
		return err
	}
	var dataMap map[string]interface{}
	if data != nil {
		dataMap = data
	} else {
		dataMap = map[string]interface{}{}
	}
	// Validate that JSON-marshalling round-trips — protects against data containing
	// non-serialisable types (channels, funcs) which would cause the repo to fail silently.
	if _, err := json.Marshal(dataMap); err != nil {
		return err
	}
	n := &domain.Notification{
		ID:        uuid.New(),
		UserID:    uID,
		Type:      nType,
		Title:     title,
		Message:   message,
		Data:      dataMap,
		Read:      false,
		CreatedAt: time.Now(),
	}
	return e.repo.Create(ctx, n)
}
