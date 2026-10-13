// Package playbackstats turns anonymous playback beacons into per-video daily
// aggregates for the creator: watch time, average view duration, an audience
// retention curve and a device split.
//
// # Privacy
//
// A sample is folded into daily counters in the same call that receives it. No
// raw sample row, session id, viewer digest, IP address or User-Agent is ever
// stored, so nothing here can be joined back to a person and there is no raw
// table to prune. The only per-request input derived from the client is a
// coarse device/OS bucket (see ClassifyUA). Country is not recorded: core has
// no GeoIP database and this package must not add one.
package playbackstats

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

const (
	// Buckets is the resolution of the retention curve.
	Buckets = 20
	// MaxWatchedPerSample bounds the seconds one beacon may claim, so a forged
	// or buggy client cannot inflate watch time by more than the heartbeat
	// interval times the rate limit.
	MaxWatchedPerSample = 30
	// WindowDays is the reporting window.
	WindowDays = 30
	// Retention is how long daily aggregates are kept.
	Retention = 400 * 24 * time.Hour
)

// Repository is satisfied by *sqlcgen.Queries; tests substitute a fake.
type Repository interface {
	UpsertVideoPlaybackDay(ctx context.Context, arg sqlcgen.UpsertVideoPlaybackDayParams) error
	UpsertVideoPlaybackDim(ctx context.Context, arg sqlcgen.UpsertVideoPlaybackDimParams) error
	ListVideoPlaybackDays(ctx context.Context, arg sqlcgen.ListVideoPlaybackDaysParams) ([]sqlcgen.ListVideoPlaybackDaysRow, error)
	SumVideoPlaybackDims(ctx context.Context, arg sqlcgen.SumVideoPlaybackDimsParams) ([]sqlcgen.SumVideoPlaybackDimsRow, error)
	PruneVideoPlaybackDays(ctx context.Context, before pgtype.Date) (int64, error)
	PruneVideoPlaybackDims(ctx context.Context, before pgtype.Date) (int64, error)
}

// ErrUnavailable is returned when no repository is wired.
var ErrUnavailable = errors.New("playbackstats: repository unavailable")

// Service records and reports playback aggregates.
type Service struct{ repo Repository }

// NewService builds a Service over repo.
func NewService(repo Repository) *Service { return &Service{repo: repo} }

// Sample is one validated beacon. DurationSeconds is the SERVER's known video
// duration (never client-supplied); Device/OS come from ClassifyUA.
type Sample struct {
	VideoID         uuid.UUID
	PositionSeconds int
	WatchedSeconds  int
	DurationSeconds int
	Started         bool
	Device          string
	OS              string
}

// Bucket maps a playhead position onto 0..Buckets-1 (clamped).
func Bucket(position, duration int) int {
	if duration <= 0 || position <= 0 {
		return 0
	}
	b := position * Buckets / duration
	if b >= Buckets {
		return Buckets - 1
	}
	return b
}

// Record folds one sample into today's aggregates.
func (s *Service) Record(ctx context.Context, in Sample) error {
	if s == nil || s.repo == nil {
		return ErrUnavailable
	}
	w := in.WatchedSeconds
	if w < 0 {
		w = 0
	}
	if w > MaxWatchedPerSample {
		w = MaxWatchedPerSample
	}
	var plays int64
	if in.Started {
		plays = 1
	}
	if plays == 0 && w == 0 {
		return nil
	}
	if err := s.repo.UpsertVideoPlaybackDay(ctx, sqlcgen.UpsertVideoPlaybackDayParams{
		VideoID: in.VideoID, Plays: plays, WatchSeconds: int64(w),
	}); err != nil {
		return err
	}
	if w == 0 {
		return nil
	}
	dev, osf := in.Device, in.OS
	if !deviceKeys[dev] {
		dev = DeviceOther
	}
	if !osKeys[osf] {
		osf = "other"
	}
	for _, d := range []struct{ kind, key string }{
		{"retention", bucketKey(Bucket(in.PositionSeconds, in.DurationSeconds))},
		{"device", dev},
		{"os", osf},
	} {
		if err := s.repo.UpsertVideoPlaybackDim(ctx, sqlcgen.UpsertVideoPlaybackDimParams{
			VideoID: in.VideoID, Kind: d.kind, Key: d.key, Value: int64(w),
		}); err != nil {
			return err
		}
	}
	return nil
}

func bucketKey(b int) string { return string([]byte{byte('0' + b/10), byte('0' + b%10)}) }

// Prune deletes aggregates older than Retention.
func (s *Service) Prune(ctx context.Context, now time.Time) (int64, error) {
	if s == nil || s.repo == nil {
		return 0, ErrUnavailable
	}
	cut := pgtype.Date{Time: now.UTC().Add(-Retention), Valid: true}
	a, err := s.repo.PruneVideoPlaybackDays(ctx, cut)
	if err != nil {
		return 0, err
	}
	b, err := s.repo.PruneVideoPlaybackDims(ctx, cut)
	return a + b, err
}

// DayWatch is one day of the watch-time series.
type DayWatch struct {
	Day          time.Time
	Plays        int64
	WatchSeconds int64
}

// Share is one slice of a split, as watch seconds and a 0..1 fraction.
type Share struct {
	Key     string
	Seconds int64
	Share   float64
}

// Report is the owner-facing view of a video's playback aggregates.
type Report struct {
	Plays                  int64
	WatchSeconds           int64
	AvgViewDurationSeconds float64
	// Retention has Buckets entries (0..1), or is nil when the duration is
	// unknown or nothing was watched.
	Retention []float64
	Devices   []Share
	OS        []Share
	Daily     []DayWatch
}

// Report builds the last WindowDays of aggregates for videoID. duration is the
// server-known duration in seconds (0 = unknown, which omits the curve).
func (s *Service) Report(ctx context.Context, videoID uuid.UUID, duration int, now time.Time) (Report, error) {
	if s == nil || s.repo == nil {
		return Report{}, ErrUnavailable
	}
	since := pgtype.Date{Time: now.UTC().AddDate(0, 0, -(WindowDays - 1)), Valid: true}
	days, err := s.repo.ListVideoPlaybackDays(ctx, sqlcgen.ListVideoPlaybackDaysParams{VideoID: videoID, Since: since})
	if err != nil {
		return Report{}, err
	}
	dims, err := s.repo.SumVideoPlaybackDims(ctx, sqlcgen.SumVideoPlaybackDimsParams{VideoID: videoID, Since: since})
	if err != nil {
		return Report{}, err
	}
	return BuildReport(days, dims, duration), nil
}

// BuildReport is the pure rollup arithmetic.
//
// Retention[i] = seconds watched while the playhead was in the i-th twentieth
// of the video / (plays x length of a twentieth), clamped to 1. It is the mean
// fraction of that slice each play watched, so it falls where viewers leave or
// skip and (unlike a distinct-viewer count) needs no per-viewer state.
func BuildReport(days []sqlcgen.ListVideoPlaybackDaysRow, dims []sqlcgen.SumVideoPlaybackDimsRow, duration int) Report {
	r := Report{Daily: make([]DayWatch, 0, len(days))}
	for _, d := range days {
		r.Plays += d.Plays
		r.WatchSeconds += d.WatchSeconds
		r.Daily = append(r.Daily, DayWatch{Day: d.Day.Time, Plays: d.Plays, WatchSeconds: d.WatchSeconds})
	}
	if r.Plays > 0 {
		r.AvgViewDurationSeconds = float64(r.WatchSeconds) / float64(r.Plays)
	}
	var ret [Buckets]int64
	devices, oses := map[string]int64{}, map[string]int64{}
	for _, d := range dims {
		switch d.Kind {
		case "retention":
			if len(d.Key) == 2 {
				if i := int(d.Key[0]-'0')*10 + int(d.Key[1]-'0'); i >= 0 && i < Buckets {
					ret[i] += d.Value
				}
			}
		case "device":
			devices[d.Key] += d.Value
		case "os":
			oses[d.Key] += d.Value
		}
	}
	if duration > 0 && r.Plays > 0 && r.WatchSeconds > 0 {
		slice := float64(duration) / Buckets
		r.Retention = make([]float64, Buckets)
		for i, v := range ret {
			f := float64(v) / (float64(r.Plays) * slice)
			if f > 1 {
				f = 1
			}
			r.Retention[i] = f
		}
	}
	r.Devices, r.OS = shares(devices), shares(oses)
	return r
}

func shares(m map[string]int64) []Share {
	var total int64
	for _, v := range m {
		total += v
	}
	out := make([]Share, 0, len(m))
	for k, v := range m {
		sh := Share{Key: k, Seconds: v}
		if total > 0 {
			sh.Share = float64(v) / float64(total)
		}
		out = append(out, sh)
	}
	// Largest first; key breaks ties so the order is deterministic.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && (out[j].Seconds > out[j-1].Seconds || (out[j].Seconds == out[j-1].Seconds && out[j].Key < out[j-1].Key)); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
