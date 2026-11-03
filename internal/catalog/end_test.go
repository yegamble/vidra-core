package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/searchevents"
)

func TestFrozenEndHasExplicitEmptyManifestAndStableCompleteFraming(t *testing.T) {
	identity := Identity{ScopeLocal, uuid.New(), math.MaxInt64}
	eventID, at := uuid.New(), time.Now().UTC()
	limits := Limits{MaxRequestBytes, MaxSnapshotBytes, MaxDocuments, MaxPages}
	empty, _ := MembershipDigest(nil)
	manifest := Manifest{MembershipSHA256: empty, PagesSHA256: empty}
	frozen, err := FreezeEnd(identity, manifest, 0, eventID, at, limits)
	if err != nil {
		t.Fatal(err)
	}
	again, err := FreezeEnd(identity, manifest, 0, eventID, at, limits)
	if err != nil || !bytes.Equal(frozen.RequestBody, again.RequestBody) {
		t.Fatal("end replay changed")
	}
	var batch searchevents.EventBatch
	if err := json.Unmarshal(frozen.RequestBody, &batch); err != nil || len(batch.Events) != 1 || batch.Events[0].Type != TypeSnapshotEnd || batch.Events[0].SchemaVersion != ProtocolVersion {
		t.Fatalf("invalid end envelope: %+v, %v", batch, err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(batch.Events[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 7 || string(payload["page_count"]) != "0" || string(payload["document_count"]) != "0" || string(payload["generation"]) != `"9223372036854775807"` {
		t.Fatalf("missing explicit end fields: %s", batch.Events[0].Payload)
	}
	limits.MaxRequestBytes = int64(len(frozen.RequestBody))
	if _, err := FreezeEnd(identity, manifest, 0, eventID, at, limits); err != nil {
		t.Fatal(err)
	}
	limits.MaxRequestBytes--
	if _, err := FreezeEnd(identity, manifest, 0, eventID, at, limits); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("unbounded end frame: %v", err)
	}
}

func TestFrozenEndChecksFullCountsAndCanonicalByteQuota(t *testing.T) {
	identity := Identity{ScopeLocal, uuid.New(), 1}
	eventID, at := uuid.New(), time.Now().UTC()
	limits := Limits{MaxRequestBytes, MaxSnapshotBytes, MaxDocuments, MaxPages}
	manifest := Manifest{MaxPages, MaxDocuments, strings.Repeat("a", 64), strings.Repeat("b", 64)}
	if _, err := FreezeEnd(identity, manifest, MaxSnapshotBytes, eventID, at, limits); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		manifest       Manifest
		canonicalBytes int64
	}{
		{manifest, MaxSnapshotBytes + 1}, {manifest, 0},
		{Manifest{MaxPages + 1, MaxDocuments, manifest.MembershipSHA256, manifest.PagesSHA256}, 1},
		{Manifest{MaxPages, MaxDocuments + 1, manifest.MembershipSHA256, manifest.PagesSHA256}, 1},
	} {
		if _, err := FreezeEnd(identity, tc.manifest, tc.canonicalBytes, eventID, at, limits); !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("accepted over-budget end %+v: %v", tc, err)
		}
	}
	for _, tc := range []struct {
		identity Identity
		eventID  uuid.UUID
		at       time.Time
		manifest Manifest
	}{
		{Identity{ScopeLocal, identity.Epoch, 0}, eventID, at, manifest},
		{identity, uuid.Nil, at, manifest}, {identity, eventID, time.Time{}, manifest},
		{identity, eventID, at, Manifest{}},
	} {
		if _, err := FreezeEnd(tc.identity, tc.manifest, 1, tc.eventID, tc.at, limits); err == nil {
			t.Fatalf("accepted invalid end %+v", tc)
		}
	}
}
