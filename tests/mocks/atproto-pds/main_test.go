package main

import (
	"bytes"
	"encoding/json"
	"fmt"
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

	recordBody := fmt.Sprintf(`{"repo":"did:plc:test123","collection":"app.bsky.feed.post","record":{"text":"hello world"}}`)
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
