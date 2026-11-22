package ipfs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthenticatedKuboRPC(t *testing.T) {
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	token := hex.EncodeToString(random[:])
	redirect := false
	calls := 0
	node := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("missing RPC authorization")
		}
		if redirect {
			http.Redirect(w, r, "/other", http.StatusTemporaryRedirect)
			return
		}
		_, _ = w.Write([]byte(`{"RepoSize":2048}`))
	}))
	defer node.Close()
	client := NewKuboClient(node.URL, node.Client(), WithRPCBearer(token))
	if size, err := client.RepoSize(context.Background()); size != 2048 || err != nil {
		t.Fatalf("authenticated repo/stat: size=%d error=%v", size, err)
	}
	redirect = true
	if _, err := client.RepoSize(context.Background()); err == nil || strings.Contains(err.Error(), token) || calls != 2 {
		t.Fatalf("redirect accepted, credential disclosed, or unexpected request count: %d", calls)
	}
	plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("sent a credential over HTTP") }))
	defer plain.Close()
	if _, err := NewKuboClient(plain.URL, nil, WithRPCBearer(token)).RepoSize(context.Background()); err == nil {
		t.Fatal("authenticated cleartext RPC accepted")
	}
}
