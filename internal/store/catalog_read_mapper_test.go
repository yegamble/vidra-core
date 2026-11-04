package store

import (
	"math"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestCatalogReadProofMapperPreservesComparableFullAndViewerPolicy(t *testing.T) {
	empty := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	for _, hide := range []bool{false, true} {
		for _, manifest := range []catalog.Manifest{{MembershipSHA256: empty, PagesSHA256: empty},
			{PageCount: catalog.MaxPages, DocumentCount: catalog.MaxDocuments, MembershipSHA256: strings.Repeat("a", 64), PagesSHA256: strings.Repeat("b", 64)}} {
			r := sqlcgen.ReadCatalogPublicProofRow{Epoch: uuid.New(), Generation: math.MaxInt64, PageCount: manifest.PageCount,
				DocumentCount: manifest.DocumentCount, MembershipSha256: manifest.MembershipSHA256, PagesSha256: manifest.PagesSHA256, HideSensitive: hide}
			want := catalog.ReadProof{Full: catalog.FullProof{Identity: catalog.Identity{Scope: catalog.ScopeLocal, Epoch: r.Epoch, Generation: math.MaxInt64}, Manifest: manifest}, HideSensitive: hide}
			if got, err := mapCatalogReadProof(r); err != nil || got != want {
				t.Fatalf("valid source proof changed: %+v/%v, want %+v", got, err, want)
			}
		}
	}
}

func TestCatalogReadProofMapperRefusesMalformedEvidenceWithoutPartialProof(t *testing.T) {
	valid := sqlcgen.ReadCatalogPublicProofRow{Epoch: uuid.New(), Generation: 9007199254740993, PageCount: 1,
		DocumentCount: 1, MembershipSha256: strings.Repeat("a", 64), PagesSha256: strings.Repeat("b", 64), HideSensitive: true}
	for _, change := range []func(*sqlcgen.ReadCatalogPublicProofRow){
		func(r *sqlcgen.ReadCatalogPublicProofRow) { r.Epoch = uuid.Nil },
		func(r *sqlcgen.ReadCatalogPublicProofRow) { r.Generation = 0 },
		func(r *sqlcgen.ReadCatalogPublicProofRow) { r.Generation = -1 },
		func(r *sqlcgen.ReadCatalogPublicProofRow) { r.PageCount = -1 },
		func(r *sqlcgen.ReadCatalogPublicProofRow) { r.DocumentCount = -1 },
		func(r *sqlcgen.ReadCatalogPublicProofRow) { r.PageCount = 0 },
		func(r *sqlcgen.ReadCatalogPublicProofRow) { r.PageCount = 2 },
		func(r *sqlcgen.ReadCatalogPublicProofRow) { r.DocumentCount = 0 },
		func(r *sqlcgen.ReadCatalogPublicProofRow) {
			r.PageCount, r.DocumentCount = catalog.MaxPages+1, catalog.MaxPages+1
		},
		func(r *sqlcgen.ReadCatalogPublicProofRow) { r.DocumentCount = catalog.MaxDocuments + 1 },
		func(r *sqlcgen.ReadCatalogPublicProofRow) { r.DocumentCount = math.MaxInt64 },
		func(r *sqlcgen.ReadCatalogPublicProofRow) { r.MembershipSha256 = strings.Repeat("A", 64) },
		func(r *sqlcgen.ReadCatalogPublicProofRow) { r.PagesSha256 = "" },
		func(r *sqlcgen.ReadCatalogPublicProofRow) { r.MembershipSha256 = strings.Repeat("z", 64) },
		func(r *sqlcgen.ReadCatalogPublicProofRow) { r.PageCount, r.DocumentCount = 0, 0 },
	} {
		r := valid
		change(&r)
		if got, err := mapCatalogReadProof(r); err == nil || got != (catalog.ReadProof{}) {
			t.Fatalf("malformed row manufactured partial proof: %+v/%v from %+v", got, err, r)
		}
	}
}
