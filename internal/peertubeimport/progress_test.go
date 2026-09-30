package peertubeimport

import "testing"

func TestProgressSnapshotsDoNotChangeUnderReaders(t *testing.T) {
	r := NewReport(false, PolicySkip, false)
	var snapshots []*Report
	r.onProgress = func(snapshot *Report) { snapshots = append(snapshots, snapshot) }
	r.count(KindHLSPlaylist).Imported = 1
	r.Deferred = []string{"deferred"}
	r.Conflicts = []string{"conflict"}
	r.publishProgress()
	r.count(KindHLSPlaylist).Imported++
	r.Deferred[0], r.Conflicts[0] = "changed", "changed"
	r.publishProgress()
	if snapshots[0].Entities[KindHLSPlaylist].Imported != 1 || snapshots[1].Entities[KindHLSPlaylist].Imported != 2 || snapshots[0].Deferred[0] != "deferred" || snapshots[0].Conflicts[0] != "conflict" {
		t.Fatal("a published snapshot changed after later progress")
	}
	snapshots[0].publishProgress()
	if len(snapshots) != 2 {
		t.Fatal("snapshot retained the run's callback")
	}
}
