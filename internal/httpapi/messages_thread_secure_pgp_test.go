//go:build pgp
// +build pgp

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
	"github.com/ProtonMail/gopenpgp/v2/pgp"
	"github.com/google/uuid"
)

func TestSecureThread_WithRealPGP_PerRecipientDecryptionAndConversationID(t *testing.T) {
	td := testutil.SetupTestDB(t)
	if td == nil {
		return
	}
	td.TruncateTables(t, "messages", "conversations", "users")

	ur := repository.NewUserRepository(td.DB)
	mr := repository.NewMessageRepository(td.DB)
	svc := usecase.NewMessageServiceWithPGPConfig(mr, ur, crypto.NewPGPService(), &config.Config{PGPVerifySignatures: true})

	ctx := context.Background()
	now := time.Now()
	alice := &domain.User{ID: uuid.NewString(), Username: "alice_pgp_real", Email: "a@e.com", Role: domain.RoleUser, IsActive: true, CreatedAt: now, UpdatedAt: now}
	bob := &domain.User{ID: uuid.NewString(), Username: "bob_pgp_real", Email: "b@e.com", Role: domain.RoleUser, IsActive: true, CreatedAt: now, UpdatedAt: now}
	if err := ur.Create(ctx, alice, "hash"); err != nil {
		t.Fatalf("create alice: %v", err)
	}
	if err := ur.Create(ctx, bob, "hash"); err != nil {
		t.Fatalf("create bob: %v", err)
	}

	// Generate real keypairs and lock with distinct passphrases
	pgpsvc := crypto.NewPGPService()
	// Alice
	apub, apriv, _, err := pgpsvc.GenerateKeyPair("Alice", "a@e.com")
	if err != nil {
		t.Fatalf("gen alice: %v", err)
	}
	alock := lockPrivate(t, apriv, "alice-passphrase")
	// Bob
	bpub, bpriv, _, err := pgpsvc.GenerateKeyPair("Bob", "b@e.com")
	if err != nil {
		t.Fatalf("gen bob: %v", err)
	}
	block := lockPrivate(t, bpriv, "bob-92321")

	// Store pub keys on server
	set := SetPGPKeyHandler(svc)
	for id, pub := range map[string]string{alice.ID: apub, bob.ID: bpub} {
		body := map[string]any{"pgp_public_key": pub}
		bts, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/pgp", bytes.NewReader(bts))
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, id))
		rr := httptest.NewRecorder()
		set.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("set pgp: %d %s", rr.Code, rr.Body.String())
		}
	}

	sendSecure := SendSecureMessageHandler(svc)
	var firstConv string
	total := 10
	for i := 0; i < total; i++ {
		// Alternate sender/recipient
		var fromUser, toUser *domain.User
		var fromPriv, toPub, fromPass string
		if i%2 == 0 {
			fromUser, toUser = alice, bob
			fromPriv, fromPass, toPub = alock, "alice-passphrase", bpub
		} else {
			fromUser, toUser = bob, alice
			fromPriv, fromPass, toPub = block, "bob-92321", apub
		}
		plaintext := []byte("hello-" + fromUser.Username)
		// Encrypt to recipient
		enc := encryptArmored(t, toPub, plaintext)
		// Sign the encrypted content with sender's private
		sig := signArmored(t, pgpsvc, enc, fromPriv, fromPass)
		// Send
		body := map[string]any{"recipient_id": toUser.ID, "encrypted_content": enc, "pgp_signature": sig}
		bts, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/messages/secure", bytes.NewReader(bts))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, fromUser.ID))
		rr := httptest.NewRecorder()
		sendSecure.ServeHTTP(rr, req)
		if rr.Code != http.StatusCreated {
			t.Fatalf("send secure failed: %d %s", rr.Code, rr.Body.String())
		}
		// Assert conversation_id present in send response
		env := decodeEnv(t, rr)
		var mr domain.MessageResponse
		_ = json.Unmarshal(mustJSON(t, env.Data), &mr)
		if mr.Message.ConversationID == "" {
			t.Fatalf("expected conversation_id on send")
		}
		if i == 0 {
			firstConv = mr.Message.ConversationID
		}
	}

	// Check conversation list contains the same secure conversation id
	getConvs := GetConversationsHandler(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations?limit=50", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, alice.ID))
	rr := httptest.NewRecorder()
	getConvs.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("conv list: %d %s", rr.Code, rr.Body.String())
	}
	env := decodeEnv(t, rr)
	var cres domain.ConversationsResponse
	_ = json.Unmarshal(mustJSON(t, env.Data), &cres)
	var found bool
	for _, c := range cres.Conversations {
		if c.ID == firstConv && c.IsSecureMode {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("secure conversation id not found in list")
	}

	// Fetch messages as each user and decrypt messages where they are recipient
	getMsgs := GetMessagesHandler(svc)
	for _, who := range []struct {
		user       *domain.User
		priv, pass string
	}{
		{alice, alock, "alice-passphrase"}, {bob, block, "bob-92321"},
	} {
		other := alice
		if who.user.ID == alice.ID {
			other = bob
		}
		r := httptest.NewRequest(http.MethodGet, "/api/v1/messages?conversation_with="+other.ID+"&limit=100", nil)
		r = r.WithContext(context.WithValue(r.Context(), middleware.UserIDKey, who.user.ID))
		rr2 := httptest.NewRecorder()
		getMsgs.ServeHTTP(rr2, r)
		if rr2.Code != http.StatusOK {
			t.Fatalf("get msgs: %d %s", rr2.Code, rr2.Body.String())
		}
		env2 := decodeEnv(t, rr2)
		var mres domain.MessagesResponse
		_ = json.Unmarshal(mustJSON(t, env2.Data), &mres)
		if len(mres.Messages) != total {
			t.Fatalf("expected %d msgs, got %d", total, len(mres.Messages))
		}
		// Decrypt only messages where who is recipient
		for _, m := range mres.Messages {
			if m.RecipientID != who.user.ID {
				continue
			}
			if m.EncryptedContent == nil {
				t.Fatalf("expected encrypted content")
			}
			plain, derr := pgpsvc.DecryptMessage(*m.EncryptedContent, who.priv, who.pass)
			if derr != nil {
				t.Fatalf("decrypt failed: %v", derr)
			}
			if len(plain) == 0 {
				t.Fatalf("empty plaintext after decryption")
			}
		}
	}
}

func lockPrivate(t *testing.T, armorPriv, pass string) string {
	t.Helper()
	k, err := pgp.NewKeyFromArmored(armorPriv)
	if err != nil {
		t.Fatalf("parse priv: %v", err)
	}
	locked, err := k.Lock([]byte(pass))
	if err != nil {
		t.Fatalf("lock priv: %v", err)
	}
	s, err := locked.Armor()
	if err != nil {
		t.Fatalf("armor locked: %v", err)
	}
	return s
}

func encryptArmored(t *testing.T, pub string, data []byte) string {
	t.Helper()
	k, err := pgp.NewKeyFromArmored(pub)
	if err != nil {
		t.Fatalf("parse pub: %v", err)
	}
	ring, err := pgp.NewKeyRing(k)
	if err != nil {
		t.Fatalf("ring: %v", err)
	}
	em, err := ring.Encrypt(pgp.NewPlainMessage(data), nil)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	s, err := em.GetArmored()
	if err != nil {
		t.Fatalf("armor enc: %v", err)
	}
	return s
}

func signArmored(t *testing.T, svc *crypto.PGPService, message, armorPriv, pass string) string {
	t.Helper()
	sig, err := svc.SignMessage(message, armorPriv, pass)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return sig
}
