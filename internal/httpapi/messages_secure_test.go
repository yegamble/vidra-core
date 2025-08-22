package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/middleware"
	"athena/internal/repository"
	"athena/internal/testutil"
	"athena/internal/usecase"
)

func TestSetAndRemovePGPKey_AndSendSecureMessage(t *testing.T) {
	td := testutil.SetupTestDB(t)
	if td == nil {
		return
	}
	td.TruncateTables(t, "messages", "conversations", "users")

	userRepo := repository.NewUserRepository(td.DB)
	msgRepo := repository.NewMessageRepository(td.DB)
	svc := usecase.NewMessageService(msgRepo, userRepo)

	ctx := context.Background()
	alice := createTestUser(t, userRepo, ctx, "alice_pgp", "alice_pgp@example.com")
	bob := createTestUser(t, userRepo, ctx, "bob_pgp", "bob_pgp@example.com")

	// Set PGP keys for both users
	set := SetPGPKeyHandler(svc)
	pub := "-----BEGIN PGP PUBLIC KEY BLOCK-----\n...\n-----END PGP PUBLIC KEY BLOCK-----"
	for _, u := range []string{alice.ID, bob.ID} {
		body := map[string]any{"pgp_public_key": pub}
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/pgp", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, u))
		rr := httptest.NewRecorder()
		set.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("set pgp key failed: %d %s", rr.Code, rr.Body.String())
		}
	}

	// Send a secure message from Alice to Bob
	send := SendSecureMessageHandler(svc)
	body := map[string]any{"recipient_id": bob.ID, "encrypted_content": "[Encrypted]hello", "pgp_signature": "sig"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/messages/secure", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, alice.ID))
	rr := httptest.NewRecorder()
	send.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("send secure failed: %d %s", rr.Code, rr.Body.String())
	}

	// Remove Alice key
	remove := RemovePGPKeyHandler(svc)
	req2 := httptest.NewRequest(http.MethodDelete, "/api/v1/users/me/pgp", nil)
	req2 = req2.WithContext(context.WithValue(req2.Context(), middleware.UserIDKey, alice.ID))
	rr2 := httptest.NewRecorder()
	remove.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("remove pgp key failed: %d", rr2.Code)
	}
}
