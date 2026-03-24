// Package main implements a lightweight mock ATProto PDS server for integration testing.
// It handles the XRPC endpoints used by internal/usecase/atproto_service.go.
package main

import (
	"crypto/rand"
	"crypto/sha256"
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
	handles       map[string]string // did -> handle
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

func didForHandle(handle string) string {
	normalized := strings.ToLower(strings.TrimSpace(handle))
	switch normalized {
	case "", "alice.bsky.social":
		return "did:plc:test123"
	case "test.handle":
		return "did:plc:testhandle"
	}

	sum := sha256.Sum256([]byte(normalized))
	return "did:plc:" + hex.EncodeToString(sum[:8])
}

func newState() *state {
	return &state{
		accessTokens:  make(map[string]string),
		refreshTokens: make(map[string]string),
		handles:       make(map[string]string),
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
//
//nolint:gocyclo // Keeping the mock route wiring in one place makes this test server easier to follow.
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
		did := didForHandle(req.Identifier)
		s.mu.Lock()
		s.handles[did] = req.Identifier
		s.mu.Unlock()
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
		tokenDID, ok := s.validateAccess(token)
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

		repoDID := strings.TrimSpace(req.Repo)
		if repoDID == "" {
			repoDID = tokenDID
		}

		cid := "bafyreid" + randomToken()[:12]
		uri := fmt.Sprintf("at://%s/%s/%s", repoDID, req.Collection, randomToken()[:8])
		entry := recordEntry{
			Repo:       repoDID,
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

	// Get record — returns a record by repo/collection/rkey
	mux.HandleFunc("/xrpc/com.atproto.repo.getRecord", func(w http.ResponseWriter, r *http.Request) {
		repo := r.URL.Query().Get("repo")
		collection := r.URL.Query().Get("collection")
		rkey := r.URL.Query().Get("rkey")
		if repo == "" || collection == "" || rkey == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing repo, collection, or rkey"})
			return
		}
		targetURI := fmt.Sprintf("at://%s/%s/%s", repo, collection, rkey)
		s.mu.Lock()
		var found *recordEntry
		for i := range s.records {
			if s.records[i].URI == targetURI {
				found = &s.records[i]
				break
			}
		}
		s.mu.Unlock()
		if found == nil {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"uri":   targetURI,
				"cid":   "bafyreid" + randomToken()[:12],
				"value": map[string]interface{}{},
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"uri":   found.URI,
			"cid":   found.CID,
			"value": found.Record,
		})
	})

	// Resolve handle — returns DID for a given handle
	mux.HandleFunc("/xrpc/com.atproto.identity.resolveHandle", func(w http.ResponseWriter, r *http.Request) {
		handle := r.URL.Query().Get("handle")
		if handle == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing handle"})
			return
		}
		did := didForHandle(handle)
		s.mu.Lock()
		s.handles[did] = handle
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"did": did,
		})
	})

	// Get profile — returns profile details for a resolved DID
	mux.HandleFunc("/xrpc/app.bsky.actor.getProfile", func(w http.ResponseWriter, r *http.Request) {
		actor := r.URL.Query().Get("actor")
		if actor == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing actor"})
			return
		}

		s.mu.Lock()
		handle := s.handles[actor]
		s.mu.Unlock()
		if handle == "" {
			handle = "alice.bsky.social"
		}

		displayName := strings.Split(handle, ".")[0]
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"did":         actor,
			"handle":      handle,
			"displayName": displayName,
			"description": "Mock ATProto profile",
		})
	})

	// Get post thread — returns thread view for a given URI
	mux.HandleFunc("/xrpc/app.bsky.feed.getPostThread", func(w http.ResponseWriter, r *http.Request) {
		uri := r.URL.Query().Get("uri")
		if uri == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing uri"})
			return
		}
		s.mu.Lock()
		replies := []interface{}{}
		for _, rec := range s.records {
			if rec.Record != nil {
				if reply, ok := rec.Record["reply"].(map[string]interface{}); ok {
					if parent, ok := reply["parent"].(map[string]interface{}); ok {
						if parentURI, _ := parent["uri"].(string); parentURI == uri {
							replies = append(replies, map[string]interface{}{
								"$type": "app.bsky.feed.defs#threadViewPost",
								"post": map[string]interface{}{
									"uri":    rec.URI,
									"cid":    rec.CID,
									"record": rec.Record,
								},
							})
						}
					}
				}
			}
		}
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"thread": map[string]interface{}{
				"$type": "app.bsky.feed.defs#threadViewPost",
				"post": map[string]interface{}{
					"uri": uri,
					"cid": "bafyreid" + randomToken()[:12],
				},
				"replies": replies,
			},
		})
	})

	// Delete record — requires valid Bearer access token
	mux.HandleFunc("/xrpc/com.atproto.repo.deleteRecord", func(w http.ResponseWriter, r *http.Request) {
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
		var req struct {
			Repo       string `json:"repo"`
			Collection string `json:"collection"`
			Rkey       string `json:"rkey"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		targetURI := fmt.Sprintf("at://%s/%s/%s", req.Repo, req.Collection, req.Rkey)
		s.mu.Lock()
		for i := range s.records {
			if s.records[i].URI == targetURI {
				s.records = append(s.records[:i], s.records[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
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
