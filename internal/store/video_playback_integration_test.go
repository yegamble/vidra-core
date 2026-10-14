//go:build integration

// Integration test: the playback-aggregate upserts against a REAL PostgreSQL
// with migration 0174 applied. The properties no fake can prove are the
// ON CONFLICT accumulation (two samples must SUM, not overwrite) and that
// pruning removes only days older than the cut.
package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/playbackstats"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestVideoPlaybackAggregatesAccumulateAndPrune(t *testing.T) {
	ctx := context.Background()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)
	vid, cleanup := seedVideoRow(t, st)
	t.Cleanup(cleanup)

	svc := playbackstats.NewService(st.Queries())
	for _, s := range []playbackstats.Sample{
		{VideoID: vid, PositionSeconds: 5, WatchedSeconds: 10, DurationSeconds: 200, Started: true, Device: "mobile", OS: "ios"},
		{VideoID: vid, PositionSeconds: 6, WatchedSeconds: 10, DurationSeconds: 200, Device: "mobile", OS: "ios"},
	} {
		if err := svc.Record(ctx, s); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	rep, err := svc.Report(ctx, vid, 200, time.Now())
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.Plays != 1 || rep.WatchSeconds != 20 || len(rep.Devices) != 1 || rep.Devices[0].Seconds != 20 || rep.Retention[0] != 1 {
		t.Fatalf("report = %+v", rep)
	}

	// An old day must be pruned, today's must survive.
	old := time.Now().UTC().AddDate(0, 0, -500)
	if _, err := st.Pool.Exec(ctx, `INSERT INTO video_playback_days (video_id, day, plays, watch_seconds) VALUES ($1, $2, 1, 1)`, vid, old); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Prune(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	days, err := st.Queries().ListVideoPlaybackDays(ctx, sqlcgen.ListVideoPlaybackDaysParams{VideoID: vid, Since: pgtype.Date{Time: old.AddDate(0, 0, -1), Valid: true}})
	if err != nil || len(days) != 1 {
		t.Fatalf("days after prune = %v err=%v", days, err)
	}
}
