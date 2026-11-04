package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/searchevents"
)

var (
	ErrScalarLimit   = errors.New("catalog source scalar exceeds receiver range")
	ErrQuotaExceeded = errors.New("catalog resource quota exceeded")
)

type FrozenPage struct {
	EventID                  uuid.UUID
	PageIndex, DocumentCount int32
	CanonicalBytes           int64
	ContentSHA256            string
	RequestBody              []byte
}

// PageBuilder freezes one request. Add returns false without mutation on overflow;
// overflow on an empty page cannot be repaired by starting another page.
type PageBuilder struct {
	identity     Identity
	index, count int32
	eventID      uuid.UUID
	at           time.Time
	limits       Limits
	base         int
	documents    []byte
	seen         map[uuid.UUID]bool
}

func NewPageBuilder(id Identity, index int32, eventID uuid.UUID, at time.Time, limits Limits) (*PageBuilder, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	limits = limits.Effective()
	if index < 0 || int64(index) >= limits.MaxPages || limits.MaxRequestBytes <= 0 || limits.MaxSnapshotBytes <= 0 || limits.MaxDocuments <= 0 {
		return nil, ErrQuotaExceeded
	}
	if eventID == uuid.Nil || at.IsZero() {
		return nil, fmt.Errorf("catalog page needs immutable event identity and time")
	}
	p := &PageBuilder{identity: id, index: index, eventID: eventID, at: at, limits: limits, documents: []byte("["), seen: make(map[uuid.UUID]bool)}
	body, err := p.request([]byte("[]"), strings.Repeat("0", 64))
	if err != nil {
		return nil, err
	}
	p.base = len(body)
	if int64(p.base) > limits.MaxRequestBytes {
		return nil, ErrQuotaExceeded
	}
	return p, nil
}

func (p *PageBuilder) Add(doc searchevents.VideoDoc) (bool, error) {
	if doc.Views < 0 || doc.Likes < 0 || doc.Likes > math.MaxInt32 || doc.DurationSeconds != nil && *doc.DurationSeconds < 0 {
		return false, ErrScalarLimit
	}
	if doc.ID == uuid.Nil || doc.Kind != p.identity.Scope || doc.Privacy != "public" || doc.State != "published" || doc.CreatedAt.IsZero() || doc.UpdatedAt.IsZero() || p.seen[doc.ID] {
		return false, fmt.Errorf("catalog page requires distinct canonical eligible documents")
	}
	raw, err := json.Marshal(struct {
		Video            searchevents.VideoDoc `json:"video"`
		Eligible         bool                  `json:"eligible"`
		SuppressedReason *string               `json:"suppressed_reason"`
	}{doc, true, nil})
	if err != nil {
		return false, err
	}
	next := len(p.documents) + 1 + len(raw) // Closing bracket; existing opening bracket remains.
	if p.count > 0 {
		next++
	}
	if int64(p.base+next-2) > p.limits.MaxRequestBytes || int64(p.count)+1 > p.limits.MaxDocuments {
		return false, nil
	}
	if p.count > 0 {
		p.documents = append(p.documents, ',')
	}
	p.documents = append(p.documents, raw...)
	p.count++
	p.seen[doc.ID] = true
	return true, nil
}

func (p *PageBuilder) Freeze() (FrozenPage, error) {
	if p.count == 0 {
		return FrozenPage{}, fmt.Errorf("catalog page must be nonempty")
	}
	raw := append(append([]byte(nil), p.documents...), ']')
	canonical, digest, err := DocumentsDigest(raw)
	if err != nil {
		return FrozenPage{}, err
	}
	body, err := p.request(canonical, digest)
	if err != nil {
		return FrozenPage{}, err
	}
	// Verify actual complete framing even though Add preflights each document.
	if int64(len(body)) > p.limits.MaxRequestBytes || int64(len(canonical)) > p.limits.MaxSnapshotBytes {
		return FrozenPage{}, ErrQuotaExceeded
	}
	return FrozenPage{p.eventID, p.index, p.count, int64(len(canonical)), digest, body}, nil
}

func (p *PageBuilder) request(documents json.RawMessage, digest string) ([]byte, error) {
	payload, err := json.Marshal(struct {
		Identity
		PageDigest
		Documents json.RawMessage `json:"documents"`
	}{p.identity, PageDigest{p.index, digest}, documents})
	if err != nil {
		return nil, err
	}
	return json.Marshal(searchevents.EventBatch{Events: []searchevents.Envelope{{
		EventID: p.eventID, Type: TypeSnapshotPage, OccurredAt: p.at, SchemaVersion: ProtocolVersion, Payload: payload,
	}}})
}
