package main

import (
	"bytes"
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

func TestCreateSession(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	body := `{"identifier":"test@example.com","password":"test-password"}`
	resp, err := http.Post(ts.URL+"/xrpc/com.atproto.server.createSession", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST createSession failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if _, ok := result["accessJwt"]; !ok {
		t.Error("response missing accessJwt")
	}
	if _, ok := result["refreshJwt"]; !ok {
		t.Error("response missing refreshJwt")
	}
	if _, ok := result["did"]; !ok {
		t.Error("response missing did")
	}
}

func TestCreateRecord_RequiresAuth(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	body := `{"repo":"did:plc:test123","collection":"app.bsky.feed.post","record":{"text":"hello"}}`
	req, _ := http.NewRequest("POST", ts.URL+"/xrpc/com.atproto.repo.createRecord", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No Authorization header

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST createRecord failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without auth, got %d", resp.StatusCode)
	}
}

func TestCreateRecord_WithAuth(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	// First create session to get tokens
	sessionBody := `{"identifier":"test@example.com","password":"test-password"}`
	sessionResp, err := http.Post(ts.URL+"/xrpc/com.atproto.server.createSession", "application/json", strings.NewReader(sessionBody))
	if err != nil {
		t.Fatalf("createSession failed: %v", err)
	}
	var session map[string]interface{}
	json.NewDecoder(sessionResp.Body).Decode(&session)
	sessionResp.Body.Close()

	accessToken, ok := session["accessJwt"].(string)
	if !ok {
		t.Fatal("no accessJwt in session")
	}

	// Now create a record with the token
	recordBody := `{"repo":"did:plc:test123","collection":"app.bsky.feed.post","record":{"$type":"app.bsky.feed.post","text":"test video","createdAt":"2026-02-21T00:00:00Z"}}`
	req, _ := http.NewRequest("POST", ts.URL+"/xrpc/com.atproto.repo.createRecord", strings.NewReader(recordBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST createRecord failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if _, ok := result["uri"]; !ok {
		t.Error("createRecord response missing uri")
	}
	if _, ok := result["cid"]; !ok {
		t.Error("createRecord response missing cid")
	}
}

func TestUploadBlob_RequiresAuth(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/xrpc/com.atproto.repo.uploadBlob", bytes.NewReader([]byte("fake-image-data")))
	req.Header.Set("Content-Type", "image/png")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST uploadBlob failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without auth, got %d", resp.StatusCode)
	}
}

func TestUploadBlob_WithAuth(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	// Create session first
	sessionBody := `{"identifier":"test@example.com","password":"test-password"}`
	sessionResp, _ := http.Post(ts.URL+"/xrpc/com.atproto.server.createSession", "application/json", strings.NewReader(sessionBody))
	var session map[string]interface{}
	json.NewDecoder(sessionResp.Body).Decode(&session)
	sessionResp.Body.Close()
	accessToken := session["accessJwt"].(string)

	// Upload a blob
	fakeImageData := bytes.Repeat([]byte{0x89, 0x50, 0x4E, 0x47}, 100) // fake PNG-ish bytes
	req, _ := http.NewRequest("POST", ts.URL+"/xrpc/com.atproto.repo.uploadBlob", bytes.NewReader(fakeImageData))
	req.Header.Set("Content-Type", "image/png")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST uploadBlob failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	blob, ok := result["blob"].(map[string]interface{})
	if !ok {
		t.Errorf("response missing blob field, got: %v", result)
		return
	}
	if blob["$type"] != "blob" {
		t.Errorf("expected blob.$type=blob, got %v", blob["$type"])
	}
}

func TestGetAuthorFeed(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/xrpc/app.bsky.feed.getAuthorFeed?actor=did:plc:test123")
	if err != nil {
		t.Fatalf("GET getAuthorFeed failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if _, ok := result["feed"]; !ok {
		t.Error("response missing feed field")
	}
}

func TestRefreshSession(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	// Create session first
	sessionBody := `{"identifier":"test@example.com","password":"test-password"}`
	sessionResp, _ := http.Post(ts.URL+"/xrpc/com.atproto.server.createSession", "application/json", strings.NewReader(sessionBody))
	var session map[string]interface{}
	json.NewDecoder(sessionResp.Body).Decode(&session)
	sessionResp.Body.Close()
	refreshToken := session["refreshJwt"].(string)

	// Refresh the session
	req, _ := http.NewRequest("POST", ts.URL+"/xrpc/com.atproto.server.refreshSession", nil)
	req.Header.Set("Authorization", "Bearer "+refreshToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST refreshSession failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if _, ok := result["accessJwt"]; !ok {
		t.Error("refresh response missing accessJwt")
	}
}

func TestRefreshSession_InvalidToken(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/xrpc/com.atproto.server.refreshSession", nil)
	req.Header.Set("Authorization", "Bearer invalid-refresh-token")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST refreshSession failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid refresh token, got %d", resp.StatusCode)
	}
}

func TestResolveHandle(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/xrpc/com.atproto.identity.resolveHandle?handle=alice.bsky.social")
	if err != nil {
		t.Fatalf("GET resolveHandle failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if _, ok := result["did"]; !ok {
		t.Error("response missing did field")
	}
}

func TestResolveHandle_MissingHandle(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/xrpc/com.atproto.identity.resolveHandle")
	if err != nil {
		t.Fatalf("GET resolveHandle failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing handle, got %d", resp.StatusCode)
	}
}

func TestGetRecord(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/xrpc/com.atproto.repo.getRecord?repo=did:plc:test123&collection=app.bsky.feed.post&rkey=abc123")
	if err != nil {
		t.Fatalf("GET getRecord failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if _, ok := result["uri"]; !ok {
		t.Error("response missing uri field")
	}
	if _, ok := result["cid"]; !ok {
		t.Error("response missing cid field")
	}
}

func TestGetPostThread(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/xrpc/app.bsky.feed.getPostThread?uri=at://did:plc:test123/app.bsky.feed.post/abc123&depth=6")
	if err != nil {
		t.Fatalf("GET getPostThread failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if _, ok := result["thread"]; !ok {
		t.Error("response missing thread field")
	}
}

func TestDeleteRecord_RequiresAuth(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	body := `{"repo":"did:plc:test123","collection":"app.bsky.feed.post","rkey":"abc123"}`
	req, _ := http.NewRequest("POST", ts.URL+"/xrpc/com.atproto.repo.deleteRecord", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No Authorization header

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST deleteRecord failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without auth, got %d", resp.StatusCode)
	}
}

func TestDeleteRecord_WithAuth(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	// Create session
	sessionBody := `{"identifier":"test@example.com","password":"test-password"}`
	sessionResp, _ := http.Post(ts.URL+"/xrpc/com.atproto.server.createSession", "application/json", strings.NewReader(sessionBody))
	var session map[string]interface{}
	json.NewDecoder(sessionResp.Body).Decode(&session)
	sessionResp.Body.Close()
	accessToken := session["accessJwt"].(string)

	// Delete record
	body := `{"repo":"did:plc:test123","collection":"app.bsky.feed.post","rkey":"abc123"}`
	req, _ := http.NewRequest("POST", ts.URL+"/xrpc/com.atproto.repo.deleteRecord", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST deleteRecord failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 200, got %d: %s", resp.StatusCode, body)
	}
}

func TestDebugRecords(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	// Create session and a record
	sessionBody := `{"identifier":"test@example.com","password":"test-password"}`
	sessionResp, _ := http.Post(ts.URL+"/xrpc/com.atproto.server.createSession", "application/json", strings.NewReader(sessionBody))
	var session map[string]interface{}
	json.NewDecoder(sessionResp.Body).Decode(&session)
	sessionResp.Body.Close()
	accessToken := session["accessJwt"].(string)

	recordBody := `{"repo":"did:plc:test123","collection":"app.bsky.feed.post","record":{"text":"hello world"}}`
	req, _ := http.NewRequest("POST", ts.URL+"/xrpc/com.atproto.repo.createRecord", strings.NewReader(recordBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	http.DefaultClient.Do(req)

	// Retrieve debug records
	resp, err := http.Get(ts.URL + "/test/records")
	if err != nil {
		t.Fatalf("GET /test/records failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var records []interface{}
	json.NewDecoder(resp.Body).Decode(&records)
	if len(records) == 0 {
		t.Error("expected at least one record in /test/records")
	}
}
