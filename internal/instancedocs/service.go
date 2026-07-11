// Package instancedocs implements the instance-document store (config-parity
// W1): long-form, admin-authored documents that live OUTSIDE the
// instancesettings registry — the custom homepage markdown and the custom
// CSS/JS injected into every page. Bodies are document-scale (up to 100–200
// KB), so they get their own table (migration 0085), per-document size caps,
// and hash-addressed public delivery instead of the registry's 10k-capped
// PATCH-scalar semantics.
//
// The service mirrors the instancesettings caching posture: every row is
// loaded at boot into an in-memory cache and reloaded after each write, so the
// hot public paths (GET /instance customization/homepage blocks,
// /instance/homepage, /instance/custom.css, /instance/custom.js) are
// lock-guarded map reads with no per-request DB round trip. Writing an empty
// body deletes the row (the public delivery routes 404 while a document is
// unset).
package instancedocs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Document names. They double as the API path segment and the DB name value.
const (
	NameHomepage  = "homepage"
	NameCustomCSS = "custom_css"
	NameCustomJS  = "custom_js"
)

// Names is the canonical document-name order (also the admin/OpenAPI order).
var Names = []string{NameHomepage, NameCustomCSS, NameCustomJS}

// maxBodyBytes caps each document's body size: the homepage is a markdown
// page (100 KB), the CSS/JS injections get more headroom (200 KB), per the
// W1<->W2 instance contract.
var maxBodyBytes = map[string]int{
	NameHomepage:  100 << 10,
	NameCustomCSS: 200 << 10,
	NameCustomJS:  200 << 10,
}

// ErrUnknownName means the document name is not one of homepage, custom_css,
// custom_js. The HTTP layer maps it to a 404.
var ErrUnknownName = errors.New("instancedocs: unknown document name")

// ValidationError is a content failure (an over-cap body) the HTTP layer maps
// to a 422 field error.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

// MaxBodyBytes reports the byte cap for a document name (0 when unknown).
func MaxBodyBytes(name string) int { return maxBodyBytes[name] }

// IsName reports whether name is a recognised document name.
func IsName(name string) bool { _, ok := maxBodyBytes[name]; return ok }

// Repository is the data access the service needs. *sqlcgen.Queries satisfies
// it directly; tests substitute an in-memory fake.
type Repository interface {
	ListInstanceDocuments(ctx context.Context) ([]sqlcgen.InstanceDocument, error)
	UpsertInstanceDocument(ctx context.Context, arg sqlcgen.UpsertInstanceDocumentParams) (sqlcgen.InstanceDocument, error)
	DeleteInstanceDocument(ctx context.Context, name string) (int64, error)
}

// Document is one stored instance document: its raw body, the lowercase-hex
// sha256 of the body (the public cache-busting hash), and the update time.
type Document struct {
	Name      string
	Body      string
	SHA256    string
	UpdatedAt time.Time
}

// Service holds the instance-document store. It is safe for concurrent use.
type Service struct {
	repo Repository

	mu    sync.RWMutex
	cache map[string]Document
}

// NewService builds the document service. Call Load once at boot (it reloads
// itself after each write) to populate the cache.
func NewService(repo Repository) *Service {
	return &Service{repo: repo, cache: map[string]Document{}}
}

// Load (re)reads every document row into the in-memory cache. A nil repository
// (route-surface tests) loads an empty cache.
func (s *Service) Load(ctx context.Context) error {
	if s.repo == nil {
		return nil
	}
	rows, err := s.repo.ListInstanceDocuments(ctx)
	if err != nil {
		return err
	}
	m := make(map[string]Document, len(rows))
	for _, r := range rows {
		if !IsName(r.Name) {
			continue // ignore unknown/legacy rows defensively
		}
		m[r.Name] = fromRow(r)
	}
	s.mu.Lock()
	s.cache = m
	s.mu.Unlock()
	return nil
}

func fromRow(r sqlcgen.InstanceDocument) Document {
	return Document{Name: r.Name, Body: r.Body, SHA256: r.Sha256, UpdatedAt: r.UpdatedAt}
}

// Get returns the stored document and whether one is set. A cleared/never-set
// document reports ok=false.
func (s *Service) Get(name string) (Document, bool) {
	s.mu.RLock()
	d, ok := s.cache[name]
	s.mu.RUnlock()
	return d, ok
}

// Hash returns the stored document's content hash ("" when unset). This is the
// value GET /instance surfaces for cache busting.
func (s *Service) Hash(name string) string {
	d, _ := s.Get(name)
	return d.SHA256
}

// Set validates and stores a document body, then refreshes the cache entry.
// An empty body clears the document (deletes the row). updatedBy is the acting
// admin (stamped on the row; it survives that admin's deletion via ON DELETE
// SET NULL). Returns the stored document (zero-valued Body/SHA256 on clear).
func (s *Service) Set(ctx context.Context, name, body string, updatedBy uuid.UUID) (Document, error) {
	capBytes, ok := maxBodyBytes[name]
	if !ok {
		return Document{}, ErrUnknownName
	}
	if s.repo == nil {
		return Document{}, errors.New("instancedocs: repository not configured")
	}
	if len(body) > capBytes {
		return Document{}, &ValidationError{Message: fmt.Sprintf("must be at most %d bytes", capBytes)}
	}
	if body == "" {
		if _, err := s.repo.DeleteInstanceDocument(ctx, name); err != nil {
			return Document{}, err
		}
		s.mu.Lock()
		delete(s.cache, name)
		s.mu.Unlock()
		return Document{Name: name}, nil
	}
	sum := sha256.Sum256([]byte(body))
	row, err := s.repo.UpsertInstanceDocument(ctx, sqlcgen.UpsertInstanceDocumentParams{
		Name:      name,
		Body:      body,
		Sha256:    hex.EncodeToString(sum[:]),
		UpdatedBy: pgtype.UUID{Bytes: updatedBy, Valid: updatedBy != uuid.Nil},
	})
	if err != nil {
		return Document{}, err
	}
	doc := fromRow(row)
	s.mu.Lock()
	s.cache[name] = doc
	s.mu.Unlock()
	return doc, nil
}
