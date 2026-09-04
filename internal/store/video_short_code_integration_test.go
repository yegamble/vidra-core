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
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
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

// TestUnionFeedCarriesShortCodeInTheRightColumn runs the UNION discovery feed
// against real PostgreSQL, with BOTH a local and a remote row in it.
//
// This is the one edit in the card change that a fake cannot police. short_code
// and sensitive_reason are both text, and both branches of the UNION had to gain
// the new column in the same position. A count or type mismatch would fail
// loudly, but a POSITION mismatch between the two branches is silent: the result
// column takes its NAME from the first branch and its VALUE, per row, from
// whichever branch produced that row. So a local-only assertion proves nothing
// about the remote branch — an earlier version of this test checked only a local
// row and passed happily against a deliberately swapped query.
//
// Hence both rows are asserted. Note the remote branch can only be guarded
// against columns holding DIFFERENT values: two adjacent ”::text literals are
// interchangeable by definition, so the assertions that bite are short_code
// against watch_url (verified by mutation), not short_code against
// sensitive_reason.
func TestUnionFeedCarriesShortCodeInTheRightColumn(t *testing.T) {
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

	const reason = "flashing-imagery-marker"
	var id uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO videos (channel_id, title, privacy, state, is_sensitive, sensitive_reason)
		 VALUES ($1, 'UnionFeedProbe', 'public', 'published', true, $2) RETURNING id`,
		channelID, reason,
	).Scan(&id); err != nil {
		t.Fatalf("seed video: %v", err)
	}
	var want string
	if err := st.Pool.QueryRow(ctx, `SELECT short_code FROM videos WHERE id=$1`, id).Scan(&want); err != nil {
		t.Fatalf("read short_code: %v", err)
	}

	// A remote row so the UNION's SECOND branch is actually represented in the
	// result set. It must come back with an EMPTY short code (no local code
	// exists) while still carrying its own other columns intact.
	const remoteActor = "https://tube.remote.example/accounts/probe"
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO remote_actors (actor_url, actor_type, preferred_username, domain, public_key_pem)
		 VALUES ($1, 'Person', 'probe', 'tube.remote.example', 'x')
		 ON CONFLICT (actor_url) DO NOTHING`, remoteActor); err != nil {
		t.Fatalf("seed remote actor: %v", err)
	}
	var remoteID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO remote_videos (object_url, remote_actor_url, title, watch_url, published_at)
		 VALUES ($1, $2, 'UnionFeedRemoteProbe', 'https://tube.remote.example/w/abc', now())
		 RETURNING id`,
		"https://tube.remote.example/videos/"+uuid.NewString(), remoteActor,
	).Scan(&remoteID); err != nil {
		t.Fatalf("seed remote video: %v", err)
	}
	defer func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM remote_actors WHERE actor_url = $1`, remoteActor)
	}()

	// include_remote exercises the UNION rather than the local-only branch.
	rows, err := q.ListPublicVideosSorted(ctx, sqlcgen.ListPublicVideosSortedParams{
		IncludeRemote: true,
		Sort:          "recent",
		ResultLimit:   100,
		ResultOffset:  0,
	})
	if err != nil {
		t.Fatalf("ListPublicVideosSorted: %v", err)
	}
	var sawLocal, sawRemote bool
	for _, r := range rows {
		switch r.ID {
		case id:
			sawLocal = true
			if r.ShortCode != want {
				t.Errorf("local feed short_code = %q, want %q", r.ShortCode, want)
			}
			if r.SensitiveReason != reason {
				t.Errorf("local feed sensitive_reason = %q, want %q — the local branch is misaligned", r.SensitiveReason, reason)
			}
		case remoteID:
			sawRemote = true
			if r.ShortCode != "" {
				t.Errorf("remote feed short_code = %q, want empty — the remote branch is misaligned", r.ShortCode)
			}
			if !r.Remote {
				t.Errorf("remote row came back with remote=false")
			}
			if r.WatchUrl != "https://tube.remote.example/w/abc" {
				t.Errorf("remote watch_url = %q — the remote branch is misaligned", r.WatchUrl)
			}
		}
	}
	if !sawLocal {
		t.Errorf("seeded local video %s did not appear in the feed", id)
	}
	if !sawRemote {
		t.Errorf("seeded remote video %s did not appear in the feed; the remote branch was never exercised", remoteID)
	}
}
