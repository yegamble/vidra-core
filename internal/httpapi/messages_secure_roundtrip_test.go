package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"athena/internal/config"
	"athena/internal/crypto"
	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/repository"
	"athena/internal/testutil"
	"athena/internal/usecase"
)

// TestSecureThread_RoundTrip_DecryptRecipients
// Sends secure messages back and forth between two users, then fetches and
// decrypts messages for each recipient to verify end-to-end secure messaging.
func TestSecureThread_RoundTrip_DecryptRecipients(t *testing.T) {
	td := testutil.SetupTestDB(t)
	if td == nil {
		return
	}
	td.TruncateTables(t, "messages", "conversations", "users")

	userRepo := repository.NewUserRepository(td.DB)
	msgRepo := repository.NewMessageRepository(td.DB)

	// Enable signature verification in service
	svc := useTestMessageServiceWithVerify(msgRepo, userRepo)
	pgpsvc := crypto.NewPGPService()

	ctx := context.Background()
	// Create two users
	alice := createTestUser(t, userRepo, ctx, "alice_pgp_rt", "alice_rt@example.com")
	bob := createTestUser(t, userRepo, ctx, "bob_pgp_rt", "bob_rt@example.com")

	// Generate keypairs for both users (works for both real and stub implementations)
	apub, apriv, _, err := pgpsvc.GenerateKeyPair("Alice RT", "alice_rt@example.com")
	if err != nil {
		t.Fatalf("generate alice key: %v", err)
	}
	bpub, bpriv, _, err := pgpsvc.GenerateKeyPair("Bob RT", "bob_rt@example.com")
	if err != nil {
		t.Fatalf("generate bob key: %v", err)
	}

	// Store public keys for both users
	set := SetPGPKeyHandler(svc)
	for id, pub := range map[string]string{alice.ID: apub, bob.ID: bpub} {
		body := map[string]any{"pgp_public_key": pub}
		bts, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/pgp", bytes.NewReader(bts))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, id))
		rr := httptest.NewRecorder()
		set.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("set pgp key failed for %s: %d %s", id, rr.Code, rr.Body.String())
		}
	}

	send := SendSecureMessageHandler(svc)
	total := 6
	var convID string
	now := time.Now()
	for i := 0; i < total; i++ {
		// Alternate sender/recipient
		var fromID, toID string
		var toPub, fromPriv string
		if i%2 == 0 {
			fromID, toID = alice.ID, bob.ID
			toPub, fromPriv = bpub, apriv
		} else {
			fromID, toID = bob.ID, alice.ID
			toPub, fromPriv = apub, bpriv
		}

		// Message unique content
		plaintext := now.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano)

		// Encrypt and sign using the service (stub or real)
		enc, sig, err := pgpsvc.EncryptAndSignMessage(plaintext, toPub, fromPriv, "")
		if err != nil {
			t.Fatalf("encrypt+sign failed: %v", err)
		}

		body := map[string]any{
			"recipient_id":      toID,
			"encrypted_content": enc,
			"pgp_signature":     sig,
		}
		bts, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/messages/secure", bytes.NewReader(bts))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, fromID))
		rr := httptest.NewRecorder()
		send.ServeHTTP(rr, req)
		if rr.Code != http.StatusCreated {
			t.Fatalf("send secure failed: %d %s", rr.Code, rr.Body.String())
		}

		// Decode and capture conversation_id
		env := decodeEnv(t, rr)
		var mr domain.MessageResponse
		_ = json.Unmarshal(mustJSON(t, env.Data), &mr)
		if mr.Message.ConversationID == "" {
			t.Fatalf("expected conversation_id in send response")
		}
		if i == 0 {
			convID = mr.Message.ConversationID
		}
	}

	// Fetch and decrypt as each user for messages they received
	get := GetMessagesHandler(svc)
	for _, who := range []struct {
		userID string
		priv   string
	}{{alice.ID, apriv}, {bob.ID, bpriv}} {
		other := bob.ID
		if who.userID == bob.ID {
			other = alice.ID
		}
		req := httptest.NewRequest(http.MethodGet, "/api/v1/messages?conversation_with="+other+"&limit=100", nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, who.userID))
		rr := httptest.NewRecorder()
		get.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("get msgs failed: %d %s", rr.Code, rr.Body.String())
		}
		env := decodeEnv(t, rr)
		var res domain.MessagesResponse
		_ = json.Unmarshal(mustJSON(t, env.Data), &res)
		if len(res.Messages) != total {
			t.Fatalf("expected %d messages, got %d", total, len(res.Messages))
		}
		// All messages should share the same conversation_id
		for _, m := range res.Messages {
			if m.ConversationID == "" || m.ConversationID != convID {
				t.Fatalf("unexpected conversation id on message: %q vs %q", m.ConversationID, convID)
			}
		}
		// Decrypt messages where this user is the recipient
		for _, m := range res.Messages {
			if m.RecipientID != who.userID {
				continue
			}
			if m.EncryptedContent == nil {
				t.Fatalf("expected encrypted content for secure message")
			}
			plain, derr := pgpsvc.DecryptMessage(*m.EncryptedContent, who.priv, "")
			if derr != nil {
				t.Fatalf("decrypt failed: %v", derr)
			}
			if len(plain) == 0 {
				t.Fatalf("empty plaintext after decryption")
			}
		}
	}
}

// useTestMessageServiceWithVerify constructs a message service with signature verification enabled.
func useTestMessageServiceWithVerify(mr repository.MessageRepository, ur repository.UserRepository) *usecase.MessageService {
	return usecase.NewMessageServiceWithPGPConfig(mr, ur, crypto.NewPGPService(), &config.Config{PGPVerifySignatures: true})
}
