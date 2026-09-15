//go:build integration

// Migration-pair test for 0147 (authored comments on remote videos). The DOWN
// migration is the interesting half: an authored-remote-comment watched-word match
// row has BOTH comment_id and video_id NULL, so once the down migration drops
// watched_word_matches.authored_remote_comment_id, the restored two-target CHECK
// ((comment_id IS NULL) <> (video_id IS NULL)) evaluates FALSE for that row and the
// ADD CONSTRAINT would abort — unless the down migration deletes those rows first.
// This test seeds exactly that row and asserts the down migration SUCCEEDS.
//
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	go test -tags=integration ./internal/store/ -run TestMigration0147AuthoredRemoteCommentsDown
package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestMigration0147AuthoredRemoteCommentsDown(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	rnd := make([]byte, 6)
	if _, err := rand.Read(rnd); err != nil {
		t.Fatalf("rand: %v", err)
	}
	tempDB := "vidra_migtest_arc_" + hex.EncodeToString(rnd)

	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect maintenance db: %v", err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{tempDB}.Sanitize()); err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	defer func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer dropCancel()
		if _, err := admin.Exec(dropCtx, `DROP DATABASE IF EXISTS `+pgx.Identifier{tempDB}.Sanitize()+` WITH (FORCE)`); err != nil {
			t.Logf("cleanup: drop temp db %s: %v", tempDB, err)
		}
	}()

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	u.Path = "/" + tempDB
	conn, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatalf("connect temp db: %v", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = conn.Close(ctx)
		}
	}()

	// Apply the FULL up chain (0147 included).
	ups := loadUpMigrations(t)
	for _, m := range ups {
		applyMigration(ctx, t, conn, m)
	}

	// Seed a watched-word match whose ONLY target is an authored remote comment —
	// the row shape the down migration must not choke on.
	var userID uuid.UUID
	if err := conn.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1,$2,$3) RETURNING id`,
		"arcmig", "arcmig@example.test", "not-a-real-hash",
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	const actorURL = "https://peer.example/video-channels/films"
	if _, err := conn.Exec(ctx,
		`INSERT INTO remote_actors (actor_url, actor_type, public_key_pem) VALUES ($1,$2,$3)`,
		actorURL, "Group", "-----BEGIN PUBLIC KEY-----\nnot-a-real-key\n-----END PUBLIC KEY-----",
	); err != nil {
		t.Fatalf("seed remote_actor: %v", err)
	}
	var remoteVideoID uuid.UUID
	if err := conn.QueryRow(ctx,
		`INSERT INTO remote_videos (object_url, remote_actor_url) VALUES ($1,$2) RETURNING id`,
		"https://peer.example/videos/abc", actorURL,
	).Scan(&remoteVideoID); err != nil {
		t.Fatalf("seed remote_video: %v", err)
	}
	var commentID uuid.UUID
	if err := conn.QueryRow(ctx,
		`INSERT INTO authored_remote_comments (remote_video_id, user_id, body, object_url, in_reply_to)
		 VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		remoteVideoID, userID, "flagged body", "https://home.example/remote-comments/x", "https://peer.example/videos/abc",
	).Scan(&commentID); err != nil {
		t.Fatalf("seed authored_remote_comment: %v", err)
	}
	var wordID uuid.UUID
	if err := conn.QueryRow(ctx,
		`INSERT INTO watched_words (word, created_by) VALUES ($1,$2) RETURNING id`,
		"flagged", userID,
	).Scan(&wordID); err != nil {
		t.Fatalf("seed watched_word: %v", err)
	}
	if _, err := conn.Exec(ctx,
		`INSERT INTO watched_word_matches (watched_word_id, authored_remote_comment_id, matched_text, matched_term)
		 VALUES ($1,$2,$3,$4)`,
		wordID, commentID, "flagged body", "flagged",
	); err != nil {
		t.Fatalf("seed watched_word_match (authored remote comment): %v", err)
	}

	// Apply the 0147 DOWN migration. Before the DELETE fix this aborted on the
	// restored two-target CHECK; it must now succeed with the match row present.
	downPath := filepath.Join("..", "..", "migrations", "0147_authored_remote_comments.down.sql")
	downSQL, err := os.ReadFile(downPath)
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	if _, err := conn.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("0147 down migration failed with an authored-remote-comment match row present: %v", err)
	}

	// The table is gone, the column is gone, and the original two-target CHECK is
	// back (so a comment/video match still inserts, and a both-NULL row is refused).
	var tableOID *string
	if err := conn.QueryRow(ctx, `SELECT to_regclass('authored_remote_comments')::text`).Scan(&tableOID); err != nil {
		t.Fatalf("to_regclass: %v", err)
	}
	if tableOID != nil {
		t.Errorf("authored_remote_comments still exists after down: %q", *tableOID)
	}
	var hasCol bool
	if err := conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		 WHERE table_name='watched_word_matches' AND column_name='authored_remote_comment_id')`,
	).Scan(&hasCol); err != nil {
		t.Fatalf("column check: %v", err)
	}
	if hasCol {
		t.Error("watched_word_matches.authored_remote_comment_id still present after down")
	}
	var constraintDef string
	if err := conn.QueryRow(ctx,
		`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='watched_word_matches_one_target'`,
	).Scan(&constraintDef); err != nil {
		t.Fatalf("restored constraint missing: %v", err)
	}

	_ = conn.Close(ctx)
	closed = true
}
