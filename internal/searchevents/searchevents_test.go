package searchevents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeOutbox implements OutboxRepository + DrainRepository in memory.
type fakeOutbox struct {
	enqueued []sqlcgen.EnqueueSearchEventParams
	enqErr   error

	docs    map[uuid.UUID]sqlcgen.GetVideoSearchDocRow
	docErr  error
	allDocs []sqlcgen.ListVideoSearchDocsPageRow

	due             []sqlcgen.ClaimDueSearchEventsRow
	claimErr        error
	sawLeaseSeconds int32
	delivered       []int64
	rescheduled     map[int64]sqlcgen.RescheduleSearchEventParams
	deadLettered    map[int64]string
}

func newFakeOutbox() *fakeOutbox {
	return &fakeOutbox{
		docs:         map[uuid.UUID]sqlcgen.GetVideoSearchDocRow{},
		rescheduled:  map[int64]sqlcgen.RescheduleSearchEventParams{},
		deadLettered: map[int64]string{},
	}
}

func (f *fakeOutbox) EnqueueSearchEvent(_ context.Context, a sqlcgen.EnqueueSearchEventParams) error {
	if f.enqErr != nil {
		return f.enqErr
	}
	f.enqueued = append(f.enqueued, a)
	return nil
}

func (f *fakeOutbox) GetVideoSearchDoc(_ context.Context, id uuid.UUID) (sqlcgen.GetVideoSearchDocRow, error) {
	if f.docErr != nil {
		return sqlcgen.GetVideoSearchDocRow{}, f.docErr
	}
	row, ok := f.docs[id]
	if !ok {
		return sqlcgen.GetVideoSearchDocRow{}, errors.New("not found")
	}
	return row, nil
}

func (f *fakeOutbox) ListVideoSearchDocsPage(_ context.Context, a sqlcgen.ListVideoSearchDocsPageParams) ([]sqlcgen.ListVideoSearchDocsPageRow, error) {
	sorted := append([]sqlcgen.ListVideoSearchDocsPageRow{}, f.allDocs...)
	sort.Slice(sorted, func(i, j int) bool {
		return bytes.Compare(sorted[i].ID[:], sorted[j].ID[:]) < 0
	})
	var out []sqlcgen.ListVideoSearchDocsPageRow
	for _, r := range sorted {
		if bytes.Compare(r.ID[:], a.After[:]) > 0 {
			out = append(out, r)
		}
		if len(out) >= int(a.PageSize) {
			break
		}
	}
	return out, nil
}

// ClaimDueSearchEvents records the lease it was asked for so a test can assert
// the drainer requests one at all.
func (f *fakeOutbox) ClaimDueSearchEvents(_ context.Context, arg sqlcgen.ClaimDueSearchEventsParams) ([]sqlcgen.ClaimDueSearchEventsRow, error) {
	f.sawLeaseSeconds = arg.LeaseSeconds
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	if int(arg.BatchSize) < len(f.due) {
		return f.due[:arg.BatchSize], nil
	}
	return f.due, nil
}

func (f *fakeOutbox) MarkSearchEventDelivered(_ context.Context, id int64) error {
	f.delivered = append(f.delivered, id)
	return nil
}

func (f *fakeOutbox) RescheduleSearchEvent(_ context.Context, a sqlcgen.RescheduleSearchEventParams) error {
	f.rescheduled[a.ID] = a
	return nil
}

func (f *fakeOutbox) DeadLetterSearchEvent(_ context.Context, a sqlcgen.DeadLetterSearchEventParams) error {
	f.deadLettered[a.ID] = a.LastError
	return nil
}

// fakeSender records batches and returns a canned result/err. sent keeps every
// batch, not just the last, because one Drain may now issue several requests.
type fakeSender struct {
	result EventBatchResult
	err    error
	calls  int
	last   EventBatch
	sent   []EventBatch
}

func (s *fakeSender) SendEvents(_ context.Context, b EventBatch) (EventBatchResult, error) {
	s.calls++
	s.last = b
	s.sent = append(s.sent, b)
	return s.result, s.err
}

type fakeMetrics struct{ enqueue, deadLetter map[string]int }

func newFakeMetrics() *fakeMetrics {
	return &fakeMetrics{enqueue: map[string]int{}, deadLetter: map[string]int{}}
}
func (m *fakeMetrics) IncSearchEnqueueFailure(t string) { m.enqueue[t]++ }
func (m *fakeMetrics) IncSearchDeadLetter(t string)     { m.deadLetter[t]++ }

// --- enqueuer tests ---

func TestEnqueueVideoUpsertBuildsFullDoc(t *testing.T) {
	repo := newFakeOutbox()
	id := uuid.New()
	cat := "15"
	lic := "7"
	dur := int32(120)
	repo.docs[id] = sqlcgen.GetVideoSearchDocRow{
		ID: id, ChannelID: uuid.New(), Title: "Hello", Description: "desc",
		Privacy: "public", State: "published", Category: &cat, License: &lic, DurationSeconds: &dur,
		ChannelHandle: "ada", ChannelName: "Ada", OwnerID: uuid.New(),
		Views: 42, Likes: 7, Tags: []string{"go", "concurrency"},
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	enq := NewEnqueuer(repo, testLogger())
	enq.EnqueueVideoUpsert(context.Background(), id)

	if len(repo.enqueued) != 1 {
		t.Fatalf("enqueued = %d, want 1", len(repo.enqueued))
	}
	ev := repo.enqueued[0]
	if ev.EventType != TypeVideoUpsert {
		t.Fatalf("type = %q, want %q", ev.EventType, TypeVideoUpsert)
	}
	var p videoUpsertPayload
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Video.Title != "Hello" || p.Video.Views != 42 || p.Video.Likes != 7 {
		t.Errorf("doc = %+v", p.Video)
	}
	if p.Video.Kind != "local" || len(p.Video.Tags) != 2 {
		t.Errorf("kind/tags = %q / %v", p.Video.Kind, p.Video.Tags)
	}
	if p.Video.PublishedAt == nil {
		t.Errorf("published_at should be set for a published video")
	}
	// license rides the doc: the search service filters on it, and it is only
	// ever populated from this projection.
	if p.Video.License == nil || *p.Video.License != "7" {
		t.Errorf("license = %v, want 7", p.Video.License)
	}
}

func TestEnqueueVideoUpsertMissingDocMeteredNotFatal(t *testing.T) {
	repo := newFakeOutbox()
	m := newFakeMetrics()
	enq := NewEnqueuer(repo, testLogger(), WithMetrics(m))
	enq.EnqueueVideoUpsert(context.Background(), uuid.New()) // no doc seeded
	if len(repo.enqueued) != 0 {
		t.Fatalf("enqueued = %d, want 0", len(repo.enqueued))
	}
	if m.enqueue[TypeVideoUpsert] != 1 {
		t.Errorf("enqueue-failure metric = %d, want 1", m.enqueue[TypeVideoUpsert])
	}
}

func TestEnqueueUserPurgeEmitsSuppressAndHistory(t *testing.T) {
	repo := newFakeOutbox()
	enq := NewEnqueuer(repo, testLogger())
	uid := uuid.New()
	enq.EnqueueUserPurge(context.Background(), uid)
	if len(repo.enqueued) != 2 {
		t.Fatalf("enqueued = %d, want 2", len(repo.enqueued))
	}
	if repo.enqueued[0].EventType != TypeUserSuppress || repo.enqueued[1].EventType != TypeUserHistoryDel {
		t.Fatalf("types = %q, %q", repo.enqueued[0].EventType, repo.enqueued[1].EventType)
	}
	var sp userSuppressPayload
	_ = json.Unmarshal(repo.enqueued[0].Payload, &sp)
	if !sp.Unlisted || sp.UserID != uid {
		t.Errorf("suppress = %+v", sp)
	}
	var hp userHistoryDeletedPayload
	_ = json.Unmarshal(repo.enqueued[1].Payload, &hp)
	if hp.Scope != HistoryScopeAll {
		t.Errorf("history scope = %q, want all", hp.Scope)
	}
}

func TestNilEnqueuerIsNoop(t *testing.T) {
	var enq *Enqueuer
	// Must not panic.
	enq.EnqueueVideoUpsert(context.Background(), uuid.New())
	enq.EnqueueVideoSuppress(context.Background(), uuid.New(), SuppressDeleted)
	enq.EnqueueConfigUpdated(context.Background(), SearchConfig{})
}

func TestRunReconcileEmitsBeginPagesEnd(t *testing.T) {
	repo := newFakeOutbox()
	for i := 0; i < 3; i++ {
		repo.allDocs = append(repo.allDocs, sqlcgen.ListVideoSearchDocsPageRow{
			ID: uuid.New(), Title: "v", Privacy: "public", State: "published",
			CreatedAt: time.Now(), UpdatedAt: time.Now(), Tags: []string{},
		})
	}
	enq := NewEnqueuer(repo, testLogger())
	if err := enq.RunReconcile(context.Background(), 2); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// begin + page(2) + page(1) + end = 4 events (pageSize 2 over 3 docs).
	if len(repo.enqueued) != 4 {
		t.Fatalf("events = %d, want 4: %v", len(repo.enqueued), types(repo.enqueued))
	}
	if repo.enqueued[0].EventType != TypeReconcileBegin {
		t.Errorf("first = %q, want begin", repo.enqueued[0].EventType)
	}
	last := repo.enqueued[len(repo.enqueued)-1]
	if last.EventType != TypeReconcileEnd {
		t.Fatalf("last = %q, want end", last.EventType)
	}
	var end reconcileEndPayload
	_ = json.Unmarshal(last.Payload, &end)
	if end.Total != 3 {
		t.Errorf("total = %d, want 3", end.Total)
	}
}

func types(evs []sqlcgen.EnqueueSearchEventParams) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.EventType
	}
	return out
}

// --- drainer tests ---

func dueRows(n int) []sqlcgen.ClaimDueSearchEventsRow {
	rows := make([]sqlcgen.ClaimDueSearchEventsRow, n)
	for i := range rows {
		rows[i] = sqlcgen.ClaimDueSearchEventsRow{
			ID: int64(i + 1), EventID: uuid.New(), EventType: TypeVideoUpsert,
			Payload: []byte(`{}`), CreatedAt: time.Now(),
		}
	}
	return rows
}

func TestDrainHappyMarksAllDelivered(t *testing.T) {
	repo := newFakeOutbox()
	repo.due = dueRows(3)
	sender := &fakeSender{result: EventBatchResult{Accepted: 3}}
	d := NewDrainer(repo, sender, testLogger())
	n, err := d.Drain(context.Background(), 200)
	if err != nil || n != 3 {
		t.Fatalf("drain = %d, %v; want 3, nil", n, err)
	}
	if len(repo.delivered) != 3 {
		t.Errorf("delivered = %v, want 3", repo.delivered)
	}
	if sender.last.Events[0].SchemaVersion != SchemaVersion {
		t.Errorf("schema_version not stamped")
	}
}

// A claimed batch whose payloads exceed the per-request budget must be split
// across several requests, each within the budget. Before this, one Drain sent
// everything in a single request: on a real catalogue that was megabytes of
// reconcile pages, the receiver answered 413, and the whole batch — including
// small unrelated events sharing it — retried to the cap and dead-lettered, so
// search silently never indexed.
func TestDrainSplitsOversizedBatchAcrossRequests(t *testing.T) {
	const (
		rowPayload = 300 << 10 // 300 KiB each
		rowCount   = 8         // 2.4 MiB total, well over the 1 MiB budget
	)
	repo := newFakeOutbox()
	repo.due = dueRows(rowCount)
	for i := range repo.due {
		repo.due[i].Payload = make([]byte, rowPayload)
	}
	sender := &fakeSender{result: EventBatchResult{Accepted: rowCount}}
	d := NewDrainer(repo, sender, testLogger())

	n, err := d.Drain(context.Background(), 200)
	if err != nil {
		t.Fatalf("drain error: %v", err)
	}
	if n != rowCount {
		t.Fatalf("delivered = %d, want %d", n, rowCount)
	}
	if sender.calls < 2 {
		t.Fatalf("sent %d request(s); an oversized batch must be split", sender.calls)
	}

	seen := 0
	for i, b := range sender.sent {
		size := 0
		for _, e := range b.Events {
			size += len(e.Payload)
		}
		seen += len(b.Events)
		// A lone row over budget is allowed through (it cannot be split); any
		// request carrying more than one event must respect the budget.
		if len(b.Events) > 1 && size > maxRequestPayloadBytes {
			t.Errorf("request %d carried %d bytes over %d events, budget %d",
				i, size, len(b.Events), maxRequestPayloadBytes)
		}
	}
	if seen != rowCount {
		t.Errorf("requests carried %d events in total, want %d", seen, rowCount)
	}
}

// A single event larger than the budget is still attempted, alone, so it
// dead-letters on its own rather than taking unrelated events down with it.
func TestDrainSendsAnOversizedSingleEventAlone(t *testing.T) {
	repo := newFakeOutbox()
	repo.due = dueRows(3)
	repo.due[1].Payload = make([]byte, 2*maxRequestPayloadBytes)
	sender := &fakeSender{result: EventBatchResult{Accepted: 3}}
	d := NewDrainer(repo, sender, testLogger())

	if _, err := d.Drain(context.Background(), 200); err != nil {
		t.Fatalf("drain error: %v", err)
	}
	for _, b := range sender.sent {
		for _, e := range b.Events {
			if len(e.Payload) > maxRequestPayloadBytes && len(b.Events) != 1 {
				t.Fatalf("oversized event shared a request with %d others", len(b.Events)-1)
			}
		}
	}
}

func TestDrainPerEventFailureRetriesOnlyThatOne(t *testing.T) {
	repo := newFakeOutbox()
	repo.due = dueRows(3)
	badID := repo.due[1].EventID
	sender := &fakeSender{result: EventBatchResult{
		Accepted: 2,
		Failed:   []FailedEvent{{EventID: badID, Code: "bad", Message: "nope"}},
	}}
	d := NewDrainer(repo, sender, testLogger())
	n, _ := d.Drain(context.Background(), 200)
	if n != 2 {
		t.Fatalf("delivered = %d, want 2", n)
	}
	if _, ok := repo.rescheduled[2]; !ok {
		t.Errorf("row 2 should be rescheduled, got %v", repo.rescheduled)
	}
}

func TestDrainTransportFailureReschedulesWholeBatch(t *testing.T) {
	repo := newFakeOutbox()
	repo.due = dueRows(2)
	sender := &fakeSender{err: errors.New("connection refused")}
	d := NewDrainer(repo, sender, testLogger())
	n, err := d.Drain(context.Background(), 200)
	if err == nil || n != 0 {
		t.Fatalf("drain = %d, %v; want 0, err", n, err)
	}
	if len(repo.rescheduled) != 2 {
		t.Errorf("rescheduled = %d, want 2", len(repo.rescheduled))
	}
}

func TestDrainDeadLettersAfterCap(t *testing.T) {
	repo := newFakeOutbox()
	rows := dueRows(1)
	rows[0].Attempts = maxDrainAttempts - 1 // one more failure hits the cap
	repo.due = rows
	sender := &fakeSender{err: errors.New("boom")}
	m := newFakeMetrics()
	d := NewDrainer(repo, sender, testLogger(), WithDrainMetrics(m))
	_, _ = d.Drain(context.Background(), 200)
	if _, ok := repo.deadLettered[1]; !ok {
		t.Fatalf("row should be dead-lettered, got %v", repo.deadLettered)
	}
	if m.deadLetter[TypeVideoUpsert] != 1 {
		t.Errorf("dead-letter metric = %d, want 1", m.deadLetter[TypeVideoUpsert])
	}
}

func TestDrainBackoffDoublesAndCaps(t *testing.T) {
	if got := drainBackoff(1); got != drainBaseBackoff {
		t.Errorf("attempt 1 = %v, want %v", got, drainBaseBackoff)
	}
	if got := drainBackoff(2); got != 2*drainBaseBackoff {
		t.Errorf("attempt 2 = %v, want %v", got, 2*drainBaseBackoff)
	}
	if got := drainBackoff(20); got != maxDrainBackoff {
		t.Errorf("attempt 20 = %v, want cap %v", got, maxDrainBackoff)
	}
}
