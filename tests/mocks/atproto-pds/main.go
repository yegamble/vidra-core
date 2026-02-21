// Package main implements a lightweight mock ATProto PDS server for integration testing.
// It handles the XRPC endpoints used by internal/usecase/atproto_service.go.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// state holds all in-memory state for the mock PDS.
type state struct {
	mu            sync.Mutex
	accessTokens  map[string]string // accessToken -> did
	refreshTokens map[string]string // refreshToken -> did
	records       []recordEntry
	blobs         []blobEntry
}

type recordEntry struct {
	Repo       string                 `json:"repo"`
	Collection string                 `json:"collection"`
	Record     map[string]interface{} `json:"record"`
	URI        string                 `json:"uri"`
	CID        string                 `json:"cid"`
}

type blobEntry struct {
	Token    string `json:"token"`
	MimeType string `json:"mime_type"`
	Size     int    `json:"size"`
	CID      string `json:"cid"`
}

func randomToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func newState() *state {
	return &state{
		accessTokens:  make(map[string]string),
		refreshTokens: make(map[string]string),
	}
}

func (s *state) issueTokens(did string) (access, refresh string) {
	access = "access-" + randomToken()
	refresh = "refresh-" + randomToken()
	s.mu.Lock()
	s.accessTokens[access] = did
	s.refreshTokens[refresh] = did
	s.mu.Unlock()
	return access, refresh
}

func (s *state) validateAccess(token string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	did, ok := s.accessTokens[token]
	return did, ok
}

func (s *state) validateRefresh(token string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	did, ok := s.refreshTokens[token]
	return did, ok
}

func extractBearer(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// newRouter creates the HTTP mux for the mock PDS.
func newRouter() http.Handler {
	s := newState()
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	// Create session — accepts any identifier/password
	mux.HandleFunc("/xrpc/com.atproto.server.createSession", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Identifier string `json:"identifier"`
			Password   string `json:"password"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		did := "did:plc:test123"
		accessToken, refreshToken := s.issueTokens(did)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"accessJwt":  accessToken,
			"refreshJwt": refreshToken,
			"did":        did,
			"handle":     req.Identifier,
		})
	})

	// Refresh session — validates Bearer refresh token
	mux.HandleFunc("/xrpc/com.atproto.server.refreshSession", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		refreshToken := extractBearer(r)
		did, ok := s.validateRefresh(refreshToken)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid refresh token"})
			return
		}
		// Revoke old refresh token, issue new tokens
		s.mu.Lock()
		delete(s.refreshTokens, refreshToken)
		s.mu.Unlock()
		newAccess, newRefresh := s.issueTokens(did)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"accessJwt":  newAccess,
			"refreshJwt": newRefresh,
			"did":        did,
		})
	})

	// Create record — requires valid Bearer access token
	mux.HandleFunc("/xrpc/com.atproto.repo.createRecord", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		token := extractBearer(r)
		did, ok := s.validateAccess(token)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}

		var req struct {
			Repo       string                 `json:"repo"`
			Collection string                 `json:"collection"`
			Record     map[string]interface{} `json:"record"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 65536)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}

		cid := "bafyreid" + randomToken()[:12]
		uri := fmt.Sprintf("at://%s/%s/%s", did, req.Collection, randomToken()[:8])
		entry := recordEntry{
			Repo:       req.Repo,
			Collection: req.Collection,
			Record:     req.Record,
			URI:        uri,
			CID:        cid,
		}
		s.mu.Lock()
		s.records = append(s.records, entry)
		s.mu.Unlock()

		writeJSON(w, http.StatusOK, map[string]string{
			"uri": uri,
			"cid": cid,
		})
	})

	// Upload blob — requires valid Bearer access token
	mux.HandleFunc("/xrpc/com.atproto.repo.uploadBlob", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		token := extractBearer(r)
		_, ok := s.validateAccess(token)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024)) // 10MB limit
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read body"})
			return
		}

		mimeType := r.Header.Get("Content-Type")
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}

		cid := "bafyreib" + randomToken()[:12]
		entry := blobEntry{
			Token:    randomToken()[:8],
			MimeType: mimeType,
			Size:     len(body),
			CID:      cid,
		}
		s.mu.Lock()
		s.blobs = append(s.blobs, entry)
		s.mu.Unlock()

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"blob": map[string]interface{}{
				"$type": "blob",
				"ref": map[string]string{
					"$link": cid,
				},
				"mimeType": mimeType,
				"size":     len(body),
			},
		})
	})

	// Get author feed — returns empty feed (no auth required for reads)
	mux.HandleFunc("/xrpc/app.bsky.feed.getAuthorFeed", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"feed":   []interface{}{},
			"cursor": "",
		})
	})

	// Debug endpoint: list all created records
	mux.HandleFunc("/test/records", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		records := make([]recordEntry, len(s.records))
		copy(records, s.records)
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, records)
	})

	// Debug endpoint: list all uploaded blobs
	mux.HandleFunc("/test/blobs", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		blobs := make([]blobEntry, len(s.blobs))
		copy(blobs, s.blobs)
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, blobs)
	})

	return mux
}

func main() {
	port := "8080"
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      newRouter(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	log.Printf("Mock ATProto PDS listening on :%s", port)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
