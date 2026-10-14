package httpapi

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/diskspace"
)

func ptSmallExport(w io.Writer) error {
	_, err := w.Write(bytes.Repeat([]byte{'x'}, 2<<20))
	return err
}

func TestPeerTubeImportRequiresContentLength(t *testing.T) {
	env, token, tmp := ptSetup(t)
	if rec := ptUploadStream(env, token, -1, ptSmallExport); rec.Code != http.StatusLengthRequired {
		t.Fatalf("no Content-Length = %d, want 411", rec.Code)
	}
	ptNoTempLeft(t, tmp)
}

func TestPeerTubeImportSpoolSlotsBusyIs503WithRetryAfter(t *testing.T) {
	env, token, tmp := ptSetup(t)
	for i := 0; i < cap(env.srv.ptSpool.slots); i++ {
		env.srv.ptSpool.slots <- struct{}{}
	}
	rec := ptUploadStream(env, token, ptLength(ptSmallExport), ptSmallExport)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("busy = %d Retry-After=%q, want 503 with Retry-After", rec.Code, rec.Header().Get("Retry-After"))
	}
	ptNoTempLeft(t, tmp)
}

// Free space already promised to an admitted spool is not offered twice.
func TestPeerTubeImportAdmissionCountsReservedBytes(t *testing.T) {
	env, token, tmp := ptSetup(t)
	env.srv.measureDisk = func(string) (diskspace.Usage, error) {
		return diskspace.Usage{TotalBytes: 1 << 30, FreeBytes: 10 << 20}, nil
	}
	env.srv.ptSpool.reserved = 9 << 20 // another upload is mid-flight
	if rec := ptUploadStream(env, token, ptLength(ptSmallExport), ptSmallExport); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("over-reserved = %d, want 503", rec.Code)
	}
	env.srv.ptSpool.reserved = 0
	if rec := ptUploadStream(env, token, ptLength(ptSmallExport), ptSmallExport); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("admitted = %d, want 422 (not a zip)", rec.Code)
	}
	if got := env.srv.ptSpool.reserved; got != 0 {
		t.Fatalf("reservation leaked: %d bytes", got)
	}
	ptNoTempLeft(t, tmp)
}

// Free space is re-measured while the body streams; another consumer filling
// the disk mid-upload aborts it.
func TestPeerTubeImportRechecksDiskWhileWriting(t *testing.T) {
	env, token, tmp := ptSetup(t)
	env.srv.ptSpool.checkEvery = 512 << 10
	var calls atomic.Int32
	env.srv.measureDisk = func(string) (diskspace.Usage, error) {
		if calls.Add(1) == 1 {
			return diskspace.Usage{TotalBytes: 1 << 30, FreeBytes: 1 << 30}, nil
		}
		return diskspace.Usage{TotalBytes: 1 << 30, FreeBytes: 1 << 10}, nil
	}
	if rec := ptUploadStream(env, token, ptLength(ptSmallExport), ptSmallExport); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("disk filled mid-upload = %d, want 503", rec.Code)
	}
	if calls.Load() < 2 {
		t.Fatalf("disk measured %d times, want a re-measure while writing", calls.Load())
	}
	if got := env.srv.ptSpool.reserved; got != 0 {
		t.Fatalf("reservation leaked: %d bytes", got)
	}
	ptNoTempLeft(t, tmp)
}

func TestPeerTubeImportUsesConfiguredTmpDir(t *testing.T) {
	env, token, tmp := ptSetup(t)
	own := t.TempDir()
	env.srv.cfg.PeerTubeImportTmpDir = own
	var measured string
	env.srv.measureDisk = func(p string) (diskspace.Usage, error) {
		measured = p
		return diskspace.Usage{TotalBytes: 1 << 30, FreeBytes: 1 << 30}, nil
	}
	if rec := ptUploadStream(env, token, ptLength(ptLargeExport), ptLargeExport); rec.Code != http.StatusOK {
		t.Fatalf("import = %d %s", rec.Code, rec.Body.String())
	}
	if measured != own {
		t.Fatalf("measured %q, want the configured %q", measured, own)
	}
	ptNoTempLeft(t, own)
	ptNoTempLeft(t, tmp)
}

// A client that stops sending mid-body is cut off after the idle timeout and
// its spool file removed, instead of holding a slot until the request deadline.
func TestPeerTubeImportIdleUploadIsCutOff(t *testing.T) {
	env, token, tmp := ptSetup(t)
	env.srv.ptSpool.idle = 300 * time.Millisecond
	ts := httptest.NewServer(env.srv.Handler())
	defer ts.Close()

	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	go func() {
		_ = ptMultipart(pw, ptBoundary, func(w io.Writer) error {
			_, _ = w.Write(bytes.Repeat([]byte{'x'}, 64<<10))
			select {} // stall forever; the deferred pw.Close ends it
		})
	}()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/me/import/peertube", pr)
	req.ContentLength = 8 << 20
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+ptBoundary)
	req.Header.Set("Authorization", "Bearer "+token)
	done := make(chan *http.Response, 1)
	go func() {
		resp, err := ts.Client().Do(req)
		if err != nil {
			done <- nil
			return
		}
		done <- resp
	}()
	select {
	case resp := <-done:
		if resp == nil {
			t.Fatal("no response to the stalled upload")
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusRequestTimeout {
			t.Fatalf("stalled upload = %d, want 408", resp.StatusCode)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("stalled upload was not cut off")
	}
	ptNoTempLeft(t, tmp)
	if n := len(env.srv.ptSpool.slots); n != 0 {
		t.Fatalf("%d spool slots still held", n)
	}
}

func TestSweepStalePeerTubeSpools(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, age time.Duration) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		return p
	}
	stale := write("vidra-peertube-import-1.zip", 3*time.Hour)
	fresh := write("vidra-peertube-import-2.zip", 10*time.Minute)
	other := write("something-else.zip", 3*time.Hour)
	sweepStalePeerTubeSpools(dir, 2*time.Hour, time.Now(), nil)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale spool survived: %v", err)
	}
	for _, p := range []string{fresh, other} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s removed: %v", p, err)
		}
	}
}
