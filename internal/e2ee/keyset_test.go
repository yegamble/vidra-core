package e2ee

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestListMessagesBeforeKeyset proves encrypted threads page identically to
// plaintext ones: before_id returns only older envelopes (newest first), with a
// 422-shaped InvalidError for an unknown/foreign cursor and 404-shaped
// ErrNotParticipant for an outsider.
func TestListMessagesBeforeKeyset(t *testing.T) {
	ctx := context.Background()
	clock := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	repo := newFakeRepo(func() time.Time { return clock })
	svc := NewService(repo)

	ada, bob := repo.addUser(), repo.addUser()
	adaDev := mustDevice(t, svc, ada, "ada-laptop")
	bobDev := mustDevice(t, svc, bob, "bob-phone")
	conv, err := svc.StartConversation(ctx, ada, bob)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// Ada sends three envelopes to Bob's device, clock advancing between each.
	for _, ct := range []string{"c1", "c2", "c3"} {
		clock = clock.Add(time.Minute)
		env := []Envelope{{RecipientDeviceID: bobDev.ID, MessageType: 1, Ciphertext: ct}}
		if _, err := svc.Send(ctx, ada, conv.ID, adaDev.ID, env, nil); err != nil {
			t.Fatalf("send %s: %v", ct, err)
		}
	}

	// Bob's newest-first view.
	all, err := svc.ListMessages(ctx, bob, conv.ID, 10, 0)
	if err != nil || len(all) != 3 || all[0].Ciphertext != "c3" || all[2].Ciphertext != "c1" {
		t.Fatalf("list = %+v (err %v), want [c3 c2 c1]", all, err)
	}

	// Keyset from the middle envelope (c2) → only the older one (c1).
	before, err := svc.ListMessagesBefore(ctx, bob, conv.ID, all[1].ID, 10)
	if err != nil {
		t.Fatalf("before: %v", err)
	}
	if len(before) != 1 || before[0].Ciphertext != "c1" {
		t.Fatalf("before c2 = %+v, want [c1]", before)
	}

	// Exact boundary: before the oldest envelope is empty.
	if empty, err := svc.ListMessagesBefore(ctx, bob, conv.ID, all[2].ID, 10); err != nil || len(empty) != 0 {
		t.Fatalf("before oldest = (%+v, %v), want empty", empty, err)
	}

	// Unknown cursor → InvalidError (422).
	var invalid *InvalidError
	if _, err := svc.ListMessagesBefore(ctx, bob, conv.ID, uuid.New(), 10); !errors.As(err, &invalid) {
		t.Errorf("unknown cursor err = %v, want InvalidError", err)
	}

	// A non-participant is 404-shaped even with a cursor.
	carol := repo.addUser()
	if _, err := svc.ListMessagesBefore(ctx, carol, conv.ID, all[1].ID, 10); !errors.Is(err, ErrNotParticipant) {
		t.Errorf("outsider keyset err = %v, want ErrNotParticipant", err)
	}
}
