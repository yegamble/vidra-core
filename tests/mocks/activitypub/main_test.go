package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer() *httptest.Server {
	return httptest.NewServer(newRouter())
}

func TestHealthEndpoint(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestInboxAcceptsActivity(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	activityJSON := `{
		"@context": "https://www.w3.org/ns/activitystreams",
		"type": "Follow",
		"id": "https://example.com/activities/1",
		"actor": "https://example.com/users/alice",
		"object": "https://test.local/users/testuser"
	}`

	req, _ := http.NewRequest("POST", ts.URL+"/inbox", strings.NewReader(activityJSON))
	req.Header.Set("Content-Type", "application/activity+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /inbox failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 202, got %d: %s", resp.StatusCode, body)
	}
}

func TestGetActor(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/users/testuser")
	if err != nil {
		t.Fatalf("GET /users/testuser failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var actor map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&actor)

	if actor["type"] != "Person" {
		t.Errorf("expected type=Person, got %v", actor["type"])
	}
	if _, ok := actor["publicKey"]; !ok {
		t.Error("actor response missing publicKey")
	}
	if _, ok := actor["inbox"]; !ok {
		t.Error("actor response missing inbox")
	}
}

func TestWebFinger(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/.well-known/webfinger?resource=acct:testuser@test.local")
	if err != nil {
		t.Fatalf("GET /.well-known/webfinger failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var jrd map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&jrd)

	if _, ok := jrd["subject"]; !ok {
		t.Error("WebFinger response missing subject")
	}
	if _, ok := jrd["links"]; !ok {
		t.Error("WebFinger response missing links")
	}
}

func TestNodeInfo(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/.well-known/nodeinfo")
	if err != nil {
		t.Fatalf("GET /.well-known/nodeinfo failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestDebugInbox_HasSignatureField(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	// POST an activity without a Signature header
	activityJSON := `{"@context":"https://www.w3.org/ns/activitystreams","type":"Create","id":"https://example.com/activities/2"}`
	req, _ := http.NewRequest("POST", ts.URL+"/inbox", strings.NewReader(activityJSON))
	req.Header.Set("Content-Type", "application/activity+json")
	http.DefaultClient.Do(req)

	// Check debug inbox
	resp, err := http.Get(ts.URL + "/test/inbox")
	if err != nil {
		t.Fatalf("GET /test/inbox failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var entries []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&entries)
	if len(entries) == 0 {
		t.Fatal("expected at least one entry in /test/inbox")
	}

	entry := entries[0]
	if _, ok := entry["has_signature"]; !ok {
		t.Error("inbox entry missing has_signature field")
	}
	if entry["has_signature"] != false {
		t.Error("expected has_signature=false when no Signature header sent")
	}
}

func TestDebugInbox_WithSignature(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	activityJSON := `{"@context":"https://www.w3.org/ns/activitystreams","type":"Like","id":"https://example.com/activities/3"}`
	req, _ := http.NewRequest("POST", ts.URL+"/inbox", strings.NewReader(activityJSON))
	req.Header.Set("Content-Type", "application/activity+json")
	req.Header.Set("Signature", `keyId="https://example.com/users/alice#main-key",algorithm="rsa-sha256",headers="(request-target) host date",signature="FAKESIG=="`)
	http.DefaultClient.Do(req)

	resp, err := http.Get(ts.URL + "/test/inbox")
	if err != nil {
		t.Fatalf("GET /test/inbox failed: %v", err)
	}
	defer resp.Body.Close()

	var entries []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&entries)

	// Find the entry with signature
	found := false
	for _, entry := range entries {
		if entry["has_signature"] == true {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected at least one inbox entry with has_signature=true")
	}
}
