package ipfs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	testSegCID  = "bafybeib44snfojlej6s6rj7fodgvwu3yu2xstiax67b6imkecj3hmh7ooe"
	testRootCID = "bafybeidosoqui4j57nlswzl3whsrjyc2xsytn7hmmybawofr3hv6fsggie"
)

// addServer answers /api/v0/add with the given NDJSON lines as HTTP 200 and,
// when trailer is non-empty, an X-Stream-Error trailer — the exact shape Kubo
// uses for a failure after it has started streaming (spike T11a/F2).
func addServer(t *testing.T, lines []string, trailer string) *KuboClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = r.Body.Read(make([]byte, 1))
		w.Header().Set("Trailer", streamErrorHeader)
		w.WriteHeader(http.StatusOK)
		for _, l := range lines {
			_, _ = w.Write([]byte(l + "\n"))
		}
		if trailer != "" {
			w.Header().Set(streamErrorHeader, trailer)
		}
	}))
	t.Cleanup(srv.Close)
	return NewKuboClient(srv.URL, srv.Client())
}

func dirEntries() []DirEntry {
	return []DirEntry{{Path: "hls/master.m3u8", Data: strings.NewReader("#EXTM3U")}}
}

func TestAddDirectoryFailsOnStreamErrorTrailer(t *testing.T) {
	// The segment line was already emitted: without the trailer check the segment's
	// CID would be returned as the tree root.
	c := addServer(t, []string{`{"Name":"hls/720p/seg-1.m4s","Hash":"` + testSegCID + `","Size":"10"}`}, "no space left on device")
	_, err := c.AddDirectory(context.Background(), dirEntries())
	if err == nil || !strings.Contains(err.Error(), "no space left on device") {
		t.Fatalf("AddDirectory err = %v, want the stream error", err)
	}
}

func TestAddFailsOnStreamErrorTrailer(t *testing.T) {
	c := addServer(t, []string{`{"Name":"x","Hash":"` + testSegCID + `","Size":"10"}`}, "boom")
	if _, err := c.Add(context.Background(), "x", strings.NewReader("x")); err == nil {
		t.Fatal("Add must fail when the node reports X-Stream-Error")
	}
}

func TestAddDirectoryRequiresWrapEntry(t *testing.T) {
	// Clean 200, no trailer, but the wrapping directory entry never arrived: the
	// last hash is a child, not the tree.
	c := addServer(t, []string{`{"Name":"hls/720p/seg-1.m4s","Hash":"` + testSegCID + `","Size":"10"}`}, "")
	if _, err := c.AddDirectory(context.Background(), dirEntries()); err == nil {
		t.Fatal("AddDirectory must fail without the wrap (empty-Name) entry")
	}
}

func TestAddDirectoryReturnsWrapEntry(t *testing.T) {
	c := addServer(t, []string{
		`{"Name":"hls/720p/seg-1.m4s","Hash":"` + testSegCID + `","Size":"10"}`,
		`{"Name":"","Hash":"` + testRootCID + `","Size":"20"}`,
	}, "")
	res, err := c.AddDirectory(context.Background(), dirEntries())
	if err != nil {
		t.Fatalf("AddDirectory: %v", err)
	}
	if res.CID != testRootCID || res.Size != 20 {
		t.Fatalf("got %+v, want root %s size 20", res, testRootCID)
	}
}
