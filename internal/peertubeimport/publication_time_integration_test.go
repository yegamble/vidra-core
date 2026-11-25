//go:build integration

package peertubeimport

import (
	"context"
	"os"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestImportPreservesSourcePublicationTime(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	src, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	seedPeerTube(t, ctx, src, string(hash), secretPrivKeyAlice)
	mustExec(t, ctx, src, `ALTER TABLE video ADD COLUMN IF NOT EXISTS "publishedAt" timestamptz`)
	created := time.Date(2026, 8, 30, 18, 12, 0, 0, time.UTC)
	published := time.Date(2026, 11, 22, 22, 15, 0, 0, time.UTC)
	mustExec(t, ctx, src, `UPDATE video SET "createdAt"=$1, "publishedAt"=$2 WHERE id=1`, created, published)
	imp := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicySkip})
	version, err := imp.Preflight(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run := func() {
		t.Helper()
		if _, err := imp.Run(ctx, version, nil); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want time.Time) {
		t.Helper()
		var gotCreated time.Time
		var gotPublished, gotOriginal *time.Time
		if err := dest.QueryRow(ctx, `SELECT created_at,peertube_published_at,originally_published_at FROM videos WHERE title='First Video'`).Scan(&gotCreated, &gotPublished, &gotOriginal); err != nil {
			t.Fatal(err)
		}
		if !gotCreated.Equal(created) || gotPublished == nil || !gotPublished.Equal(want) || gotOriginal == nil || gotOriginal.Year() != 2016 {
			t.Fatalf("created %v, published %v, originally published %v", gotCreated, gotPublished, gotOriginal)
		}
	}
	run()
	check(published)
	// An older release has finished importing the video already. Its completed ledger entry must permit the date backfill.
	mustExec(t, ctx, dest, `UPDATE videos SET peertube_published_at=NULL WHERE title='First Video'`)
	run()
	check(published)
	// Simulate switching to an older source. Column discovery is cached per source.
	mustExec(t, ctx, src, `ALTER TABLE video DROP COLUMN "publishedAt"`)
	imp = NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicySkip})
	run()
	check(published)
}
