//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vidra/vidra-core/internal/media"
)

func TestPublicIPFSReferenceEligibility(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	owner, channel := seedOwnerChannel(ctx, t, st, "ipfsref")
	video := seedPublishedVideo(ctx, t, st, channel, "Reference eligibility")
	master := media.HLSPrefixForGeneration(video, 1) + "/master.m3u8"
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if _, err := tx.Exec(ctx, "UPDATE videos SET transcode_generation=1 WHERE id=$1", video); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO streaming_playlists(video_id,master_key,state) VALUES($1,$2,'ready')", video, master); err != nil {
		t.Fatal(err)
	}
	assertEligible := func(t *testing.T, query pgx.Tx, expectedMaster string, want bool) {
		t.Helper()
		var eligible bool
		err := query.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ipfs_public_reference_videos WHERE video_id=$1 AND master_key=$2)`, video, expectedMaster).Scan(&eligible)
		if err != nil || eligible != want {
			t.Fatalf("eligible=%t, want %t, err=%v", eligible, want, err)
		}
	}
	assertEligible(t, tx, master, true)
	for _, tc := range []struct {
		name, change string
		subject      uuid.UUID
	}{
		{"private", "UPDATE videos SET privacy='private' WHERE id=$1", video},
		{"unlisted", "UPDATE videos SET privacy='unlisted' WHERE id=$1", video},
		{"internal", "UPDATE videos SET privacy='internal' WHERE id=$1", video},
		{"draft", "UPDATE videos SET state='draft' WHERE id=$1", video},
		{"sensitive", "UPDATE videos SET is_sensitive=true WHERE id=$1", video},
		{"sensitive flags alone", "UPDATE videos SET sensitive_flags=ARRAY['violent'] WHERE id=$1", video},
		{"inactive owner", "UPDATE users SET is_active=false WHERE id=$1", owner},
		{"deleted owner", "UPDATE users SET deleted_at=now() WHERE id=$1", owner},
		{"unlisted owner", "UPDATE users SET unlisted=true WHERE id=$1", owner},
		{"banned owner", "INSERT INTO user_bans(user_id,reason) VALUES($1,'test')", owner},
		{"blocked video", "INSERT INTO video_blocks(video_id) VALUES($1)", video},
		{"password present", "INSERT INTO video_passwords(video_id,password_hash) VALUES($1,'fixture')", video},
		{"DRM key present", "INSERT INTO video_drm_keys(video_id,key_id,content_key_sealed) VALUES($1,uuid_generate_v4(),'fixture')", video},
		{"playlist pending", "UPDATE streaming_playlists SET state='pending' WHERE video_id=$1", video},
		{"deleted playlist", "DELETE FROM streaming_playlists WHERE video_id=$1", video},
		{"new transcode queued", "UPDATE videos SET transcode_generation=2 WHERE id=$1", video},
		{"unversioned transcode", "UPDATE videos SET transcode_generation=0 WHERE id=$1", video},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sp, err := tx.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = sp.Rollback(ctx) }()
			if _, err := sp.Exec(ctx, tc.change, tc.subject); err != nil {
				t.Fatal(err)
			}
			assertEligible(t, sp, master, false)
		})
	}

	for _, wrongMaster := range []string{
		media.HLSKeyPrefix(video) + "/master.m3u8",
		media.HLSPrefixForGeneration(uuid.New(), 1) + "/master.m3u8",
		"streaming-playlists/hls/" + video.String() + "/master.m3u8",
		media.HLSPrefixForGeneration(video, 1) + "/other.m3u8",
	} {
		if _, err := tx.Exec(ctx, "UPDATE streaming_playlists SET master_key=$2 WHERE video_id=$1", video, wrongMaster); err != nil {
			t.Fatal(err)
		}
		assertEligible(t, tx, wrongMaster, false)
	}

	const next = 2
	newMaster := media.HLSPrefixForGeneration(video, next) + "/master.m3u8"
	if _, err := tx.Exec(ctx, "UPDATE videos SET transcode_generation=$2 WHERE id=$1", video, next); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "UPDATE streaming_playlists SET master_key=$2 WHERE video_id=$1", video, newMaster); err != nil {
		t.Fatal(err)
	}
	assertEligible(t, tx, master, false)
	assertEligible(t, tx, newMaster, true)
}
