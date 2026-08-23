package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/qoe"
)

const playbackHealthPath = "/api/v1/admin/qoe/playback-health"

func i32p(v int32) *int32 { return &v }

// TestAdminPlaybackHealthDefaultsToTheLast24h is the exit criterion's zero-
// argument form: an admin opens the page and sees the last 24 hours per source.
func TestAdminPlaybackHealthDefaultsToTheLast24h(t *testing.T) {
	rec := &recordingQoE{health: qoe.PlaybackHealth{
		Sources: []qoe.SourceSummary{{
			DeliverySource: qoe.SourceCDN, StartCount: 480,
			TTFF: qoe.Percentiles{P50Ms: i32p(300), P95Ms: i32p(410), P99Ms: i32p(900)},
		}},
		Buckets: []qoe.Bucket{},
	}}
	srv := qoeServer(t, rec)
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	res := getWithAuth(srv, playbackHealthPath, admin)
	if res.Code != http.StatusOK {
		t.Fatalf("GET = %d; body=%s", res.Code, res.Body.String())
	}
	if got := rec.gotIn.end.Sub(rec.gotIn.start); got != 24*time.Hour {
		t.Errorf("default window = %v, want 24h", got)
	}
	if rec.gotIn.limit != defaultQoEBucketLimit || rec.gotIn.offset != 0 {
		t.Errorf("default paging = limit %d offset %d, want %d/0", rec.gotIn.limit, rec.gotIn.offset, defaultQoEBucketLimit)
	}
	// The snake_case contract the admin page will bind to.
	body := res.Body.String()
	for _, key := range []string{`"sources"`, `"buckets"`, `"buckets_total"`, `"delivery_source"`, `"ttff"`, `"p95_ms"`} {
		if !containsAny(body, key) {
			t.Errorf("response missing %s\n%s", key, body)
		}
	}
}

// TestAdminPlaybackHealthWindowSnapsToHours: the rollups exist at hour
// resolution, so a request for 13:37 must not silently report a window it did
// not actually answer.
func TestAdminPlaybackHealthWindowSnapsToHours(t *testing.T) {
	rec := &recordingQoE{health: qoe.PlaybackHealth{Buckets: []qoe.Bucket{}}}
	srv := qoeServer(t, rec)
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	res := getWithAuth(srv, playbackHealthPath+
		"?since=2026-08-22T00:00:00Z&until=2026-08-23T13:37:00Z&limit=5&offset=7", admin)
	if res.Code != http.StatusOK {
		t.Fatalf("GET = %d; body=%s", res.Code, res.Body.String())
	}
	// The end rounds UP, so the 13:00 rollup — the most recent one there is, and
	// the one an operator watching an incident is waiting for — is included.
	wantEnd := time.Date(2026, 8, 23, 14, 0, 0, 0, time.UTC)
	if !rec.gotIn.end.Equal(wantEnd) {
		t.Errorf("window end = %v, want %v (rounded up)", rec.gotIn.end, wantEnd)
	}
	if rec.gotIn.limit != 5 || rec.gotIn.offset != 7 {
		t.Errorf("paging = limit %d offset %d, want 5/7", rec.gotIn.limit, rec.gotIn.offset)
	}
}

// TestAdminPlaybackHealthClampsPaging keeps one request from asking for the
// whole table.
func TestAdminPlaybackHealthClampsPaging(t *testing.T) {
	rec := &recordingQoE{health: qoe.PlaybackHealth{Buckets: []qoe.Bucket{}}}
	srv := qoeServer(t, rec)
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	if res := getWithAuth(srv, playbackHealthPath+"?limit=100000&offset=-5", admin); res.Code != http.StatusOK {
		t.Fatalf("GET = %d", res.Code)
	}
	if rec.gotIn.limit != maxQoEBucketLimit {
		t.Errorf("limit = %d, want the clamp of %d", rec.gotIn.limit, maxQoEBucketLimit)
	}
	if rec.gotIn.offset != 0 {
		t.Errorf("offset = %d, want 0 for a negative request", rec.gotIn.offset)
	}
}

// TestAdminPlaybackHealthRejectsBadInput: an unparseable timestamp and a window
// wider than the service will merge both come back as fixable instructions
// rather than a 500 or a timeout.
func TestAdminPlaybackHealthRejectsBadInput(t *testing.T) {
	srv := qoeServer(t, &recordingQoE{health: qoe.PlaybackHealth{Buckets: []qoe.Bucket{}}})
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if res := getWithAuth(srv, playbackHealthPath+"?since=yesterday", admin); res.Code == http.StatusOK {
		t.Error("an unparseable since was accepted")
	}

	wide := qoeServer(t, &recordingQoE{err: qoe.ErrWindowTooWide})
	wideAdmin := registerAndToken(t, wide, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	res := getWithAuth(wide, playbackHealthPath, wideAdmin)
	if res.Code == http.StatusOK || res.Code >= 500 {
		t.Errorf("too-wide window = %d, want a 4xx that says to narrow it", res.Code)
	}
}

// TestAdminPlaybackHealthAuthorization: admin-only, exactly like the admin-jobs
// endpoints it is shaped after.
func TestAdminPlaybackHealthAuthorization(t *testing.T) {
	srv := qoeServer(t, &recordingQoE{health: qoe.PlaybackHealth{Buckets: []qoe.Bucket{}}})
	_ = registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	if res := getWithAuth(srv, playbackHealthPath, bob); res.Code != http.StatusForbidden {
		t.Errorf("non-admin = %d, want 403", res.Code)
	}
	if res := getWithAuth(srv, playbackHealthPath, ""); res.Code != http.StatusUnauthorized {
		t.Errorf("anon = %d, want 401", res.Code)
	}
}

// TestAdminPlaybackHealthCarriesTheKnownUnknowns: the two dimensions that have
// no faithful value have to reach the admin page as facts, not as zeroes and
// nulls that look like bugs.
func TestAdminPlaybackHealthCarriesTheKnownUnknowns(t *testing.T) {
	hour := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	rec := &recordingQoE{health: qoe.PlaybackHealth{
		WindowStart: hour, WindowEnd: hour.Add(time.Hour),
		Sources: []qoe.SourceSummary{{DeliverySource: qoe.SourceCDN, EventCount: 10, VerifiedCount: 2}},
		Buckets: []qoe.Bucket{{
			HourBucket: hour, DeliverySource: qoe.SourceCDN, Engine: qoe.EngineNativeHLS,
			PackagingFormat: qoe.FormatHLSTS, EventCount: 10, VerifiedCount: 2,
			RenditionReportingSupported: false,
			// No rebuffers at all: the percentiles must serialise as null, not 0.
			Rebuffer:    qoe.Percentiles{},
			ErrorCounts: map[string]int64{},
		}},
	}}
	srv := qoeServer(t, rec)
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	res := getWithAuth(srv, playbackHealthPath, admin)
	if res.Code != http.StatusOK {
		t.Fatalf("GET = %d; body=%s", res.Code, res.Body.String())
	}
	var out struct {
		Sources []struct {
			VerifiedCount int64 `json:"verified_session_count"`
			EventCount    int64 `json:"event_count"`
		} `json:"sources"`
		Buckets []struct {
			RenditionReportingSupported bool `json:"rendition_reporting_supported"`
			Rebuffer                    struct {
				P95 *int32 `json:"p95_ms"`
			} `json:"rebuffer"`
		} `json:"buckets"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Buckets) != 1 || out.Buckets[0].RenditionReportingSupported {
		t.Error("native-hls bucket did not report rendition support as false")
	}
	if out.Buckets[0].Rebuffer.P95 != nil {
		t.Errorf("rebuffer p95 serialised as %d; a window with no rebuffers must be null, not 0", *out.Buckets[0].Rebuffer.P95)
	}
	if len(out.Sources) != 1 || out.Sources[0].VerifiedCount != 2 || out.Sources[0].EventCount != 10 {
		t.Errorf("verified/total = %+v; an admin must be able to see how much of this is attested", out.Sources)
	}
}
