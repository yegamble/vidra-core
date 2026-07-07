package ipfs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestFakeClusterLifecycle exercises the in-memory fake: pin → replicated, unpin
// → gone, health ok, and the Down simulation failing every RPC.
func TestFakeClusterLifecycle(t *testing.T) {
	ctx := context.Background()
	f := NewFakeClusterClient()
	cid := RawLeafCIDv1([]byte("cluster-me"))

	if err := f.ClusterPin(ctx, cid); err != nil {
		t.Fatalf("ClusterPin: %v", err)
	}
	if !f.IsClusterPinned(cid) {
		t.Error("cid not replicated after ClusterPin")
	}
	if err := f.ClusterHealth(ctx); err != nil {
		t.Errorf("ClusterHealth: %v", err)
	}
	if err := f.ClusterUnpin(ctx, cid); err != nil {
		t.Fatalf("ClusterUnpin: %v", err)
	}
	if f.IsClusterPinned(cid) {
		t.Error("cid still replicated after ClusterUnpin")
	}

	// Down ⇒ every RPC fails (cluster unreachable → mirror degrades gracefully).
	f.Down = true
	if err := f.ClusterPin(ctx, cid); err == nil {
		t.Error("ClusterPin succeeded while down")
	}
	if err := f.ClusterUnpin(ctx, cid); err == nil {
		t.Error("ClusterUnpin succeeded while down")
	}
	if err := f.ClusterHealth(ctx); err == nil {
		t.Error("ClusterHealth succeeded while down")
	}
}

// TestFakeClusterRejectsInvalidCID guards that even the fake enforces the CID
// contract before touching its pin set.
func TestFakeClusterRejectsInvalidCID(t *testing.T) {
	ctx := context.Background()
	f := NewFakeClusterClient()
	if err := f.ClusterPin(ctx, "QmNotACIDv1"); err == nil {
		t.Error("ClusterPin accepted a CIDv0")
	}
	if err := f.ClusterUnpin(ctx, "../etc/passwd"); err == nil {
		t.Error("ClusterUnpin accepted a traversal string")
	}
}

// TestKuboClusterClientRESTContract asserts the real client hits the IPFS Cluster
// REST endpoints with the right method/path and Bearer auth, against a stub
// server (no real cluster). The token is sent only as a header — never in the URL.
func TestKuboClusterClientRESTContract(t *testing.T) {
	ctx := context.Background()
	cid := RawLeafCIDv1([]byte("rest-contract"))

	var gotMethod, gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewKuboClusterClient(srv.URL, "s3cr3t-token", srv.Client())

	if err := c.ClusterPin(ctx, cid); err != nil {
		t.Fatalf("ClusterPin: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("pin method = %s, want POST", gotMethod)
	}
	if gotPath != "/pins/"+cid {
		t.Errorf("pin path = %s, want /pins/%s", gotPath, cid)
	}
	if gotAuth != "Bearer s3cr3t-token" {
		t.Errorf("auth header = %q, want Bearer token", gotAuth)
	}

	if err := c.ClusterUnpin(ctx, cid); err != nil {
		t.Fatalf("ClusterUnpin: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("unpin method = %s, want DELETE", gotMethod)
	}

	if err := c.ClusterHealth(ctx); err != nil {
		t.Fatalf("ClusterHealth: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/id" {
		t.Errorf("health = %s %s, want GET /id", gotMethod, gotPath)
	}
}

// TestKuboClusterClientNon2xxIsError proves a cluster error surfaces as an error
// (so the mirror logs it) and never echoes the raw body.
func TestKuboClusterClientNon2xxIsError(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "cluster exploded: secret-detail", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewKuboClusterClient(srv.URL, "", srv.Client())
	err := c.ClusterPin(ctx, RawLeafCIDv1([]byte("boom")))
	if err == nil {
		t.Fatal("expected an error on a 500")
	}
	if got := err.Error(); got == "" || contains(got, "secret-detail") {
		t.Errorf("error leaked the raw body: %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
