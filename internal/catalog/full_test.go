package catalog

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFullBuilderStreamsFrozenPagesAndProvesGlobalManifest(t *testing.T) {
	id, at := Identity{ScopeLocal, uuid.New(), 1}, time.Now().UTC()
	a, b := framingDoc(), framingDoc()
	a.Description = strings.Repeat("<&>", 200)
	b = a
	b.ID = uuid.New()
	limits := Limits{MaxRequestBytes, MaxSnapshotBytes, MaxDocuments, MaxPages}
	p, _ := NewPageBuilder(id, 0, uuid.New(), at, limits)
	_, _ = p.Add(a)
	one, _ := p.Freeze()
	limits.MaxRequestBytes = int64(len(one.RequestBody) + 64) // One document per page.
	var pages []FrozenPage
	full, err := NewFullBuilder(id, at, limits, func(page FrozenPage) error {
		pages = append(pages, page)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := full.Add(a); err != nil {
		t.Fatal(err)
	}
	if err := full.Add(b); err != nil {
		t.Fatal(err)
	}
	end, err := full.Finish()
	if err != nil || len(pages) != 2 || end.Manifest.PageCount != 2 || end.Manifest.DocumentCount != 2 {
		t.Fatalf("incomplete streamed full: %+v/%d, %v", end.Manifest, len(pages), err)
	}
	membership, _ := MembershipDigest([]uuid.UUID{b.ID, a.ID})
	content, _ := PagesDigest([]PageDigest{{pages[0].PageIndex, pages[0].ContentSHA256}, {pages[1].PageIndex, pages[1].ContentSHA256}})
	if end.Manifest.MembershipSHA256 != membership || end.Manifest.PagesSHA256 != content || end.CanonicalBytes != pages[0].CanonicalBytes+pages[1].CanonicalBytes {
		t.Fatal("full proof omitted exact membership/content/byte sum")
	}
	if err := full.Add(a); err == nil {
		t.Fatal("accepted mutation after finalization")
	}
	for _, scenario := range []string{"duplicate", "snapshot bytes", "page count", "document count", "storage failure"} {
		t.Run(scenario, func(t *testing.T) {
			caps := limits
			injected := errors.New("page persistence failed")
			if scenario == "snapshot bytes" {
				caps.MaxSnapshotBytes = 2*one.CanonicalBytes - 1
			}
			if scenario == "page count" {
				caps.MaxPages = 1
			}
			if scenario == "document count" {
				caps.MaxDocuments = 1
			}
			full, err := NewFullBuilder(id, at, caps, func(FrozenPage) error {
				if scenario == "storage failure" {
					return injected
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := full.Add(a); err != nil {
				t.Fatal(err)
			}
			addErr := full.Add(b)
			if scenario == "duplicate" && addErr == nil {
				addErr = full.Add(a) // Duplicate from an already emitted page.
			}
			_, finishErr := full.Finish()
			if addErr == nil && finishErr == nil {
				t.Fatal("failed observation produced an end")
			}
			if scenario == "storage failure" && !errors.Is(finishErr, injected) {
				t.Fatalf("persistence failure was not retained: %v", finishErr)
			}
		})
	}
}

func TestFullBuilderEmptyAndInvalidSourceOwnership(t *testing.T) {
	id, at := Identity{ScopeLocal, uuid.New(), 1}, time.Now().UTC()
	limits := Limits{MaxRequestBytes, MaxSnapshotBytes, MaxDocuments, MaxPages}
	full, _ := NewFullBuilder(id, at, limits, func(FrozenPage) error {
		t.Fatal("empty observation emitted a page")
		return nil
	})
	end, err := full.Finish()
	empty, _ := MembershipDigest(nil)
	if err != nil || end.CanonicalBytes != 0 || end.Manifest != (Manifest{MembershipSHA256: empty, PagesSHA256: empty}) {
		t.Fatalf("invalid empty observation: %+v, %v", end, err)
	}
	for _, owner := range []bool{false, true} {
		full, _ = NewFullBuilder(id, at, limits, func(FrozenPage) error { return nil })
		doc := framingDoc()
		if owner {
			doc.OwnerID = uuid.Nil
		} else {
			doc.ChannelID = uuid.Nil
		}
		if full.Add(doc) == nil {
			t.Fatal("invalid canonical ownership accepted")
		}
		if _, err := full.Finish(); err == nil {
			t.Fatal("invalid ownership was silently omitted")
		}
	}
}
