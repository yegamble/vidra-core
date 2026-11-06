package searchevents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type sendEventsFunc func(context.Context, EventBatch) (EventBatchResult, error)

func (f sendEventsFunc) SendEvents(ctx context.Context, batch EventBatch) (EventBatchResult, error) {
	return f(ctx, batch)
}

func TestRunReconcileByteBoundedEscapedDescriptions(t *testing.T) {
	repo := newFakeOutbox()
	want := strings.Repeat("<", 5000) // Legal description; JSON expands each byte sixfold.
	for range DefaultReconcilePageSize {
		repo.allDocs = append(repo.allDocs, sqlcgen.ListVideoSearchDocsPageRow{
			ID: uuid.New(), Title: "legal video", Description: want,
			ChannelID: uuid.New(), OwnerID: uuid.New(), ChannelHandle: "channel", ChannelName: "Channel",
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
			Privacy: "public", State: "published", Tags: []string{},
		})
	}
	if err := NewEnqueuer(repo, testLogger()).RunReconcile(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	seen := map[uuid.UUID]bool{}
	endTotal := -1
	receiver := echo.New()
	// This is the receiver's real Echo middleware and shipped default limit.
	receiver.Use(middleware.BodyLimitWithConfig(middleware.BodyLimitConfig{LimitBytes: 2 << 20}))
	receiver.POST("/internal/v1/events", func(c *echo.Context) error {
		var batch EventBatch
		if err := c.Bind(&batch); err != nil {
			return err
		}
		for _, event := range batch.Events {
			switch event.Type {
			case TypeReconcilePage:
				var page reconcilePagePayload
				if err := json.Unmarshal(event.Payload, &page); err != nil {
					return err
				}
				for _, doc := range page.Videos {
					if seen[doc.ID] || doc.Description != want {
						t.Error("duplicate document or changed source description")
					}
					seen[doc.ID] = true
				}
			case TypeReconcileEnd:
				var end reconcileEndPayload
				if err := json.Unmarshal(event.Payload, &end); err != nil {
					return err
				}
				endTotal = end.Total
			}
		}
		return c.JSON(http.StatusOK, EventBatchResult{Accepted: len(batch.Events)})
	})
	post := func(body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/internal/v1/events", bytes.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		receiver.ServeHTTP(rec, req)
		return rec
	}
	if rec := post(bytes.Repeat([]byte("x"), 3<<20)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("body-limit negative control = %d", rec.Code)
	}
	for i, event := range repo.enqueued {
		repo.due = append(repo.due, sqlcgen.ClaimDueSearchEventsRow{
			ID: int64(i + 1), EventID: uuid.New(), EventType: event.EventType, Payload: event.Payload,
			CreatedAt: time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.FixedZone("", 3600)),
		})
	}
	sender := sendEventsFunc(func(_ context.Context, batch EventBatch) (EventBatchResult, error) {
		body, err := json.Marshal(batch)
		if err != nil {
			return EventBatchResult{}, err
		}
		if len(body) > maxRequestPayloadBytes {
			t.Errorf("serialized request = %d bytes, budget %d", len(body), maxRequestPayloadBytes)
		}
		rec := post(body)
		if rec.Code != http.StatusOK {
			return EventBatchResult{}, fmt.Errorf("receiver status %d", rec.Code)
		}
		var result EventBatchResult
		err = json.Unmarshal(rec.Body.Bytes(), &result)
		return result, err
	})
	n, err := NewDrainer(repo, sender, testLogger()).Drain(context.Background(), 200)
	if err != nil || n != len(repo.enqueued) || len(seen) != len(repo.allDocs) || endTotal != len(repo.allDocs) {
		t.Fatalf("drain=%d/%d, %v; docs=%d total=%d, want %d", n, len(repo.enqueued), err, len(seen), endTotal, len(repo.allDocs))
	}
}

func TestRunReconcileRejectsUnsendableDocument(t *testing.T) {
	repo := newFakeOutbox()
	repo.allDocs = []sqlcgen.ListVideoSearchDocsPageRow{{ID: uuid.New(), Description: strings.Repeat("x", maxRequestPayloadBytes)}}
	if err := NewEnqueuer(repo, testLogger()).RunReconcile(context.Background(), 0); err == nil {
		t.Fatal("oversized singleton must abort the sweep")
	}
	for _, event := range repo.enqueued {
		if event.EventType == TypeReconcilePage || event.EventType == TypeReconcileEnd {
			t.Fatalf("persisted %s after an unsendable document", event.EventType)
		}
	}
}

func TestDrainCountsEnvelopeAndBatchFraming(t *testing.T) {
	repo := newFakeOutbox()
	repo.due = dueRows(2)
	for i := range repo.due {
		repo.due[i].Payload, _ = json.Marshal(strings.Repeat("x", maxRequestPayloadBytes/2-2))
	}
	sender := &fakeSender{}
	if _, err := NewDrainer(repo, sender, testLogger()).Drain(context.Background(), 200); err != nil {
		t.Fatal(err)
	}
	for _, batch := range sender.sent {
		body, err := json.Marshal(batch)
		if err != nil || len(body) > maxRequestPayloadBytes {
			t.Fatalf("serialized request = %d bytes, %v; budget %d", len(body), err, maxRequestPayloadBytes)
		}
	}
}
