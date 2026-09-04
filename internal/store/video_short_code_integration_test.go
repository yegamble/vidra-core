//go:build integration

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/video"
)

// TestVideoShortCodeAndLegacyResolution exercises 0126/0127 against real
// PostgreSQL: the DEFAULT that mints a code without anyone asking, the CHECK and
// unique index that constrain it, both resolver queries, and the partial unique
// index that stops two videos claiming one legacy URL.
//
// The httpapi fakes mirror these queries, but a fake cannot prove the DEFAULT
// fires, that the ORDER BY actually prefers our own id namespace, or that the
// partial index is partial — all of which are properties of the SQL itself.
func TestVideoShortCodeAndLegacyResolution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	_, channelID, cleanup := seedUserAndChannel(t, st)
	defer cleanup()

	// An INSERT that never mentions short_code — the shape the PREVIOUS release's
	// generated code uses, and the reason the DEFAULT lives in the database.
	insert := func(title string, src *uuid.UUID) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO videos (channel_id, title, peertube_uuid) VALUES ($1, $2, $3) RETURNING id`,
			channelID, title, src,
		).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", title, err)
		}
		return id
	}

	sourceUUID := uuid.New()
	imported := insert("Imported", &sourceUUID)
	native := insert("Native", nil)

	// 1. Every row got a code, unasked, and it satisfies the same contract the
	//    Go-side validator enforces.
	codes := map[uuid.UUID]string{}
	for _, id := range []uuid.UUID{imported, native} {
		var code string
		if err := st.Pool.QueryRow(ctx, `SELECT short_code FROM videos WHERE id=$1`, id).Scan(&code); err != nil {
			t.Fatalf("read short_code: %v", err)
		}
		if !video.ValidShortCode(code) {
			t.Fatalf("short_code %q does not satisfy ValidShortCode; the CHECK and the Go validator disagree", code)
		}
		codes[id] = code
	}
	if codes[imported] == codes[native] {
		t.Fatal("two rows got the same code from one ALTER; the default is not per-row")
	}

	// 2. GetVideoIDByShortCode resolves, and an unknown code is ErrNoRows (which
	//    the service maps to ErrNotFound, i.e. a 404).
	if got, err := q.GetVideoIDByShortCode(ctx, codes[imported]); err != nil || got != imported {
		t.Fatalf("by short code = (%v, %v), want (%v, nil)", got, err, imported)
	}
	if _, err := q.GetVideoIDByShortCode(ctx, "abcdefghijk"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unknown code error = %v, want pgx.ErrNoRows", err)
	}

	// 3. GetVideoIDByLegacyUUID resolves an imported video by its SOURCE uuid —
	//    the value a PeerTube /w/{shortUUID} link decodes to, and deliberately
	//    NOT this row's own id.
	if got, err := q.GetVideoIDByLegacyUUID(ctx, sourceUUID); err != nil || got != imported {
		t.Fatalf("by peertube uuid = (%v, %v), want (%v, nil)", got, err, imported)
	}
	if sourceUUID == imported {
		t.Fatal("fixture is degenerate: the source uuid must differ from the local id")
	}

	// 4. ...and by our OWN id, the /videos/watch/{uuid} form remote AP servers
	//    still hold.
	if got, err := q.GetVideoIDByLegacyUUID(ctx, native); err != nil || got != native {
		t.Fatalf("by own id = (%v, %v), want (%v, nil)", got, err, native)
	}
	if _, err := q.GetVideoIDByLegacyUUID(ctx, uuid.New()); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unknown legacy uuid error = %v, want pgx.ErrNoRows", err)
	}

	// 5. Our own id namespace WINS when one value names two rows.
	//
	//    The INSERT ORDER here is the test. The colliding row goes in FIRST, so a
	//    sequential scan reaches it before the row that should win — which means
	//    dropping the query's `ORDER BY (id = $1) DESC` makes this fail. Seeding
	//    the target first instead would let the wrong query pass by luck, which
	//    is exactly what an earlier version of this test did.
	targetID := uuid.New()
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO videos (channel_id, title, peertube_uuid) VALUES ($1, 'ImportedThatClaimsAnId', $2)`,
		channelID, targetID,
	); err != nil {
		t.Fatalf("seed colliding row: %v", err)
	}
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO videos (id, channel_id, title) VALUES ($1, $2, 'OwnsThatId')`,
		targetID, channelID,
	); err != nil {
		t.Fatalf("seed target row: %v", err)
	}
	if got, err := q.GetVideoIDByLegacyUUID(ctx, targetID); err != nil || got != targetID {
		t.Fatalf("collision resolved to (%v, %v), want the own-id row %v", got, err, targetID)
	}

	// 6. The partial unique index refuses a SECOND video claiming one source
	//    uuid — the guard that stops a legacy URL redirecting to the wrong video
	//    half the time. It must surface as a unique violation the importer maps.
	_, err = st.Pool.Exec(ctx,
		`INSERT INTO videos (channel_id, title, peertube_uuid) VALUES ($1, 'Dup', $2)`,
		channelID, sourceUUID)
	if err == nil {
		t.Fatal("a duplicate peertube_uuid was accepted; two videos can claim one legacy URL")
	}
	if !pgconv.IsUniqueViolation(err) {
		t.Fatalf("duplicate peertube_uuid error = %v, want a unique violation", err)
	}

	// 7. ...but it is PARTIAL: any number of videos may have no source uuid.
	insert("NoSource1", nil)
	insert("NoSource2", nil)
}
