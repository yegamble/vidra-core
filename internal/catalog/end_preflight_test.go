package catalog

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func endPreflightFixture(t *testing.T, proof FullProof) FrozenEnd {
	t.Helper()
	canonicalBytes := proof.Manifest.DocumentCount
	frozen, err := FreezeEnd(proof.Identity, proof.Manifest, canonicalBytes, uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
		time.Date(2026, 11, 4, 1, 2, 3, 123, time.UTC), Limits{MaxRequestBytes, MaxSnapshotBytes, MaxDocuments, MaxPages})
	if err != nil {
		t.Fatal(err)
	}
	return frozen
}

func TestValidateFrozenEndPreservesBytesAndChecksEffectiveLimits(t *testing.T) {
	proof := FullProof{Identity{ScopeLocal, uuid.New(), math.MaxInt64}, Manifest{2, 3, strings.Repeat("a", 64), strings.Repeat("b", 64)}}
	frozen := endPreflightFixture(t, proof)
	body := append([]byte(" \n"), frozen.RequestBody...)
	before := bytes.Clone(body)
	limits := Limits{int64(len(body)), MaxSnapshotBytes, 3, 2}
	if id, err := ValidateFrozenEnd(body, proof, limits); err != nil || id != frozen.EventID || !bytes.Equal(body, before) {
		t.Fatalf("valid frozen bytes changed/refused: %s/%v", id, err)
	}
	for _, lower := range []Limits{
		{int64(len(body)) - 1, MaxSnapshotBytes, 3, 2}, {MaxRequestBytes, MaxSnapshotBytes, 2, 2},
		{MaxRequestBytes, MaxSnapshotBytes, 3, 1}, {0, MaxSnapshotBytes, 3, 2},
		{MaxRequestBytes, 0, 3, 2}, {MaxRequestBytes, MaxSnapshotBytes, 0, 2}, {MaxRequestBytes, MaxSnapshotBytes, 3, 0},
	} {
		if id, err := ValidateFrozenEnd(body, proof, lower); err == nil || id != uuid.Nil {
			t.Fatalf("accepted lower/invalid limit %+v: %s/%v", lower, id, err)
		}
	}
	oversized := append(bytes.Clone(body), bytes.Repeat([]byte(" "), MaxRequestBytes+1-len(body))...)
	if id, err := ValidateFrozenEnd(oversized[:MaxRequestBytes], proof, Limits{MaxRequestBytes, MaxSnapshotBytes, MaxDocuments, MaxPages}); err != nil || id != frozen.EventID {
		t.Fatalf("exact hard request ceiling refused: %s/%v", id, err)
	}
	if id, err := ValidateFrozenEnd(oversized, proof, Limits{2 * MaxRequestBytes, 2 * MaxSnapshotBytes, 2 * MaxDocuments, 2 * MaxPages}); err == nil || id != uuid.Nil {
		t.Fatal("receiver cannot raise the hard request ceiling")
	}
	empty, _ := MembershipDigest(nil)
	proof = FullProof{Identity{ScopeLocal, proof.Identity.Epoch, 9007199254740993}, Manifest{MembershipSHA256: empty, PagesSHA256: empty}}
	frozen = endPreflightFixture(t, proof)
	if id, err := ValidateFrozenEnd(frozen.RequestBody, proof, limits); err != nil || id != frozen.EventID {
		t.Fatalf("explicit empty full or exact large generation refused: %s/%v", id, err)
	}
}

func TestValidateFrozenEndRejectsChangedAuthorityAndEnvelope(t *testing.T) {
	proof := FullProof{Identity{ScopeLocal, uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"), 7}, Manifest{2, 3, strings.Repeat("a", 64), strings.Repeat("b", 64)}}
	frozen := endPreflightFixture(t, proof)
	raw := string(frozen.RequestBody)
	limits := Limits{MaxRequestBytes, MaxSnapshotBytes, MaxDocuments, MaxPages}
	for _, body := range []string{
		"null", raw + "{}", strings.Replace(raw, `"events":[`, `"events":null,"ignored":[`, 1),
		strings.Replace(raw, frozen.EventID.String(), uuid.Nil.String(), 1),
		strings.Replace(raw, frozen.EventID.String(), strings.ToUpper(frozen.EventID.String()), 1),
		strings.Replace(raw, `"schema_version":2`, `"schema_version":1`, 1),
		strings.Replace(raw, `"schema_version":2`, `"schema_version":2.0`, 1),
		strings.Replace(raw, TypeSnapshotEnd, TypeSnapshotPage, 1),
		strings.Replace(raw, `2026-11-04T01:02:03.000000123Z`, `0001-01-01T00:00:00Z`, 1),
		strings.Replace(raw, `2026-11-04T01:02:03.000000123Z`, `not-a-time`, 1),
		strings.Replace(raw, `"local"`, `"remote"`, 1),
		strings.Replace(raw, proof.Identity.Epoch.String(), strings.ToUpper(proof.Identity.Epoch.String()), 1),
		strings.Replace(raw, proof.Identity.Epoch.String(), uuid.NewString(), 1),
		strings.Replace(raw, `"generation":"7"`, `"generation":"07"`, 1),
		strings.Replace(raw, `"generation":"7"`, `"generation":7`, 1),
		strings.Replace(raw, `"generation":"7"`, `"generation":"8"`, 1),
		strings.Replace(raw, `"page_count":2`, `"page_count":1`, 1),
		strings.Replace(raw, `"document_count":3`, `"document_count":4`, 1),
		strings.Replace(raw, proof.Manifest.MembershipSHA256, strings.Repeat("c", 64), 1),
		strings.Replace(raw, proof.Manifest.PagesSHA256, strings.Repeat("d", 64), 1),
	} {
		if id, err := ValidateFrozenEnd([]byte(body), proof, limits); err == nil || id != uuid.Nil {
			t.Errorf("accepted changed frozen end: %s/%v: %s", id, err, body)
		}
	}
	for _, invalid := range []FullProof{{}, {Identity{ScopeLocal, proof.Identity.Epoch, 0}, proof.Manifest}, {proof.Identity, Manifest{}}} {
		if id, err := ValidateFrozenEnd(frozen.RequestBody, invalid, limits); err == nil || id != uuid.Nil {
			t.Errorf("invalid expected proof accepted: %+v: %s/%v", invalid, id, err)
		}
	}
}

func TestValidateFrozenEndRejectsAmbiguousIncompleteObjects(t *testing.T) {
	proof := FullProof{Identity{ScopeLocal, uuid.New(), 7}, Manifest{1, 1, strings.Repeat("a", 64), strings.Repeat("b", 64)}}
	body := endPreflightFixture(t, proof).RequestBody
	limits := Limits{MaxRequestBytes, MaxSnapshotBytes, MaxDocuments, MaxPages}
	var batch map[string]json.RawMessage
	_ = json.Unmarshal(body, &batch)
	var events []json.RawMessage
	_ = json.Unmarshal(batch["events"], &events)
	var envelope map[string]json.RawMessage
	_ = json.Unmarshal(events[0], &envelope)
	for _, object := range []json.RawMessage{body, events[0], envelope["payload"]} {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(object, &fields)
		for key, value := range fields {
			for _, replacement := range []string{
				`{"unknown":0,` + string(object[1:]),
				`{"` + key + `":` + string(value) + `,` + string(object[1:]),
				strings.Replace(string(object), `"`+key+`":`, `"`+strings.ToUpper(key)+`":`, 1),
			} {
				changed := bytes.Replace(body, object, []byte(replacement), 1)
				if id, err := ValidateFrozenEnd(changed, proof, limits); err == nil || id != uuid.Nil {
					t.Fatalf("accepted unknown/duplicate/aliased %s: %s/%v", key, id, err)
				}
			}
			for _, missing := range []bool{false, true} {
				copy := make(map[string]json.RawMessage, len(fields))
				for k, v := range fields {
					copy[k] = v
				}
				copy[key] = json.RawMessage("null")
				if missing {
					delete(copy, key)
				}
				replacement, _ := json.Marshal(copy)
				if id, err := ValidateFrozenEnd(bytes.Replace(body, object, replacement, 1), proof, limits); err == nil || id != uuid.Nil {
					t.Fatalf("accepted missing/null %s: %s/%v", key, id, err)
				}
			}
		}
	}
	for _, list := range []string{"[]", "null", "[" + string(events[0]) + "," + string(events[0]) + "]"} {
		if id, err := ValidateFrozenEnd([]byte(`{"events":`+list+`}`), proof, limits); err == nil || id != uuid.Nil {
			t.Fatal("accepted non-singleton END batch")
		}
	}
}
