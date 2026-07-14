package media

import "testing"

// BenchmarkRenderMasterPlaylist measures the pure HLS master-playlist render.
// It runs on every HLS master request the API proxies, so it is a read-hot path;
// keeping it allocation-cheap matters under playback fan-out.
func BenchmarkRenderMasterPlaylist(b *testing.B) {
	rungs := PlanHLSLadder(1920, 1080) // a full ladder, the worst case
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = renderMasterPlaylist(rungs, nil)
	}
}

// BenchmarkPlanHLSLadder measures the ladder planner (runs once per transcode).
func BenchmarkPlanHLSLadder(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = PlanHLSLadder(1920, 1080)
	}
}
