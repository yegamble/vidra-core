package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/vidra/vidra-core/internal/searchevents"
)

func TestFrozenPageBoundsEscapedLegalDocumentsWithActualMiddleware(t *testing.T) {
	limits := Limits{MaxRequestBytes, MaxSnapshotBytes, MaxDocuments, MaxPages}
	identity := Identity{ScopeLocal, uuid.New(), math.MaxInt64}
	at := time.Date(2026, 11, 3, 12, 0, 0, 999999999, time.FixedZone("maximum offset", 14*3600))
	var pageIndex int32
	newPage := func() *PageBuilder {
		t.Helper()
		p, err := NewPageBuilder(identity, pageIndex, uuid.New(), at, limits)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := newPage()
	var bodies [][]byte
	flush := func() {
		t.Helper()
		frozen, err := p.Freeze()
		if err != nil {
			t.Fatal(err)
		}
		again, err := p.Freeze()
		if err != nil || !bytes.Equal(frozen.RequestBody, again.RequestBody) {
			t.Fatal("freeze changed replay bytes")
		}
		if len(frozen.RequestBody) > MaxRequestBytes {
			t.Fatalf("request budget exceeded: %d", len(frozen.RequestBody))
		}
		var batch searchevents.EventBatch
		if err := json.Unmarshal(frozen.RequestBody, &batch); err != nil || len(batch.Events) != 1 || batch.Events[0].SchemaVersion != 2 {
			t.Fatalf("invalid frozen batch: %v", err)
		}
		var payload struct {
			Documents     json.RawMessage `json:"documents"`
			ContentSHA256 string          `json:"content_sha256"`
		}
		if err := json.Unmarshal(batch.Events[0].Payload, &payload); err != nil {
			t.Fatal(err)
		}
		canonical, digest, err := DocumentsDigest(payload.Documents)
		if err != nil || digest != frozen.ContentSHA256 || digest != payload.ContentSHA256 || int64(len(canonical)) != frozen.CanonicalBytes {
			t.Fatal("frozen content evidence mismatch")
		}
		if !bytes.Contains(canonical, []byte(`"views":9007199254740993`)) {
			t.Fatal("large counter rounded")
		}
		if bytes.Count(canonical, []byte(strings.Repeat(`\u003c`, 5000))) != int(frozen.DocumentCount) {
			t.Fatal("source description changed on the wire")
		}
		bodies = append(bodies, frozen.RequestBody)
	}
	for range 200 {
		doc := framingDoc()
		doc.Description = strings.Repeat("<", 5000) // Legal API maximum, six escaped bytes per character.
		fits, err := p.Add(doc)
		if err != nil {
			t.Fatal(err)
		}
		if !fits {
			flush()
			pageIndex++
			p = newPage()
			if fits, err = p.Add(doc); err != nil || !fits {
				t.Fatalf("single legal document rejected: %v", err)
			}
		}
	}
	flush()
	if len(bodies) < 2 {
		t.Fatal("fixture failed to exercise page splitting")
	}
	e := echo.New()
	e.Use(middleware.BodyLimit("2M"))
	e.POST("/", func(c echo.Context) error {
		_, err := io.Copy(io.Discard, c.Request().Body)
		if err != nil {
			return err
		}
		return c.NoContent(http.StatusOK)
	})
	for _, body := range append(bodies, bytes.Repeat([]byte(" "), (2<<20)+1)) {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)))
		want := http.StatusOK
		if len(body) > 2<<20 {
			want = http.StatusRequestEntityTooLarge
		}
		if rec.Code != want {
			t.Fatalf("actual middleware status=%d for %d bytes", rec.Code, len(body))
		}
	}
}

func TestPageUsesExactFramingAndRejectsScalarOverflowWithoutMutation(t *testing.T) {
	identity := Identity{ScopeLocal, uuid.New(), 1}
	eventID, at := uuid.New(), time.Now().UTC()
	limits := Limits{MaxRequestBytes, MaxSnapshotBytes, MaxDocuments, MaxPages}
	docs := []searchevents.VideoDoc{framingDoc(), framingDoc()}
	p, _ := NewPageBuilder(identity, 0, eventID, at, limits)
	for _, doc := range docs {
		if fits, err := p.Add(doc); err != nil || !fits {
			t.Fatal(err)
		}
	}
	full, err := p.Freeze()
	if err != nil {
		t.Fatal(err)
	}
	for _, delta := range []int64{0, -1} {
		limits.MaxRequestBytes = int64(len(full.RequestBody)) + delta
		p, err = NewPageBuilder(identity, 0, eventID, at, limits)
		if err != nil {
			t.Fatal(err)
		}
		if fits, err := p.Add(docs[0]); err != nil || !fits {
			t.Fatal(err)
		}
		fits, err := p.Add(docs[1])
		if err != nil || fits != (delta == 0) {
			t.Fatalf("framing delta%d: fits=%v, %v", delta, fits, err)
		}
	}
	limits.MaxRequestBytes = MaxRequestBytes
	p, _ = NewPageBuilder(identity, 0, eventID, at, limits)
	bad := framingDoc()
	bad.Likes = int64(math.MaxInt32) + 1
	if _, err := p.Add(bad); !errors.Is(err, ErrScalarLimit) || p.count != 0 {
		t.Fatalf("scalar overflow changed page: %v", err)
	}
	if _, err := p.Freeze(); err == nil {
		t.Fatal("empty page froze")
	}
	if fits, err := p.Add(docs[0]); err != nil || !fits {
		t.Fatal(err)
	}
	if _, err := p.Add(docs[0]); err == nil || p.count != 1 {
		t.Fatal("duplicate document accepted")
	}
	bad = framingDoc()
	bad.Privacy = "private"
	if _, err := p.Add(bad); err == nil {
		t.Fatal("ineligible document accepted")
	}
}

func framingDoc() searchevents.VideoDoc {
	return searchevents.VideoDoc{ID: uuid.New(), Kind: ScopeLocal, ChannelID: uuid.New(), OwnerID: uuid.New(),
		ChannelHandle: "handle", ChannelName: "channel", Title: "title <&>\u2028", Tags: []string{"tag"},
		Privacy: "public", State: "published", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		Views: 9007199254740993, Likes: math.MaxInt32}
}
