package peertubearchive

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
)

type entry struct {
	name string
	body string
	mode os.FileMode
}

func buildZip(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(e.body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestReadHappyPath(t *testing.T) {
	raw := buildZip(t,
		entry{name: "peertube/channels.json", body: `{"channels":[{"name":"my_chan","displayName":"My","description":"d"}]}`},
		entry{name: "peertube/video-playlists.json", body: `{"videoPlaylists":[{"displayName":"P","description":"x","privacy":1,"type":1,"elements":[{},{}]}]}`},
		entry{name: "peertube/user-settings.json", body: `{"autoPlayNextVideo":true,"email":"a@b.c"}`},
		entry{name: "peertube/videos.json", body: `{"videos":[{},{},{}]}`},
		entry{name: "peertube/following.json", body: `{"following":[{}]}`},
		entry{name: "files/videos/x.mp4", body: "ignored"},
	)
	a, err := Read(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Channels) != 1 || a.Channels[0].Name != "my_chan" {
		t.Fatalf("channels: %+v", a.Channels)
	}
	if len(a.Playlists) != 1 || a.Playlists[0].Elements != 2 || a.Playlists[0].Privacy != 1 {
		t.Fatalf("playlists: %+v", a.Playlists)
	}
	if a.Settings == nil || a.Settings.AutoPlayNextVideo == nil || !*a.Settings.AutoPlayNextVideo {
		t.Fatalf("settings: %+v", a.Settings)
	}
	if a.Skipped["videos"] != 3 || a.Skipped["following"] != 1 {
		t.Fatalf("skipped: %+v", a.Skipped)
	}
}

func TestReadFollowingHandles(t *testing.T) {
	raw := buildZip(t, entry{name: "peertube/following.json",
		body: `{"following":[{"targetHandle":"a@x.example"},{"targetHandle":" "},{},{"targetHandle":"b@y.example"}]}`})
	a, err := Read(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Following) != 2 || a.Following[1] != "b@y.example" || a.Skipped["following"] != 2 {
		t.Fatalf("following: %+v skipped %+v", a.Following, a.Skipped)
	}
}

func TestReadRejectsHostileArchives(t *testing.T) {
	good := entry{name: "peertube/channels.json", body: `{"channels":[]}`}
	many := []entry{good}
	for i := 0; i <= MaxEntries; i++ {
		many = append(many, entry{name: fmt.Sprintf("f/%d", i)})
	}
	cases := map[string][]byte{
		"not a zip":        []byte("PK nope"),
		"empty":            buildZip(t),
		"traversal":        buildZip(t, good, entry{name: "../evil.json", body: "{}"}),
		"nested traversal": buildZip(t, good, entry{name: "a/../../evil", body: "{}"}),
		"absolute":         buildZip(t, good, entry{name: "/etc/passwd", body: "x"}),
		"backslash":        buildZip(t, good, entry{name: `a\..\b`, body: "x"}),
		"drive letter":     buildZip(t, good, entry{name: "C:/x", body: "x"}),
		"nul":              buildZip(t, good, entry{name: "a\x00b", body: "x"}),
		"symlink":          buildZip(t, good, entry{name: "peertube/link", body: "/etc/passwd", mode: os.ModeSymlink | 0o777}),
		"device":           buildZip(t, good, entry{name: "dev", mode: os.ModeDevice | 0o600}),
		"duplicate":        buildZip(t, good, entry{name: "peertube/Channels.json", body: `{}`}, entry{name: "peertube/channels.json", body: `{}`}),
		"too many entries": buildZip(t, many...),
		"oversize entry":   buildZip(t, entry{name: "peertube/channels.json", body: `{"channels":[],"pad":"` + strings.Repeat("a", MaxJSONBytes) + `"}`}),
		"bad json":         buildZip(t, entry{name: "peertube/channels.json", body: `{`}),
		"no peertube data": buildZip(t, entry{name: "readme.txt", body: "hi"}),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Read(raw); !errors.Is(err, ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}
}

// A header that LIES about its uncompressed size (small) over a deflate stream
// that expands past every cap must still be refused, and must not be read whole.
func TestReadRefusesLyingSizeBomb(t *testing.T) {
	var comp bytes.Buffer
	fw, _ := flate.NewWriter(&comp, flate.BestCompression)
	chunk := bytes.Repeat([]byte{' '}, 1<<20)
	for i := 0; i < 40; i++ {
		_, _ = fw.Write(chunk)
	}
	_ = fw.Close()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	h := &zip.FileHeader{
		Name: "peertube/channels.json", Method: zip.Deflate,
		CRC32: crc32.ChecksumIEEE(nil), CompressedSize64: uint64(comp.Len()), UncompressedSize64: 10,
	}
	w, err := zw.CreateRaw(h)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(comp.Bytes())
	_ = zw.Close()

	if _, err := Read(buf.Bytes()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

func TestReadEnforcesDeclaredTotal(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := 0; i < 9; i++ {
		h := &zip.FileHeader{Name: fmt.Sprintf("peertube/%d", i), Method: zip.Store, UncompressedSize64: 8 << 20, CompressedSize64: 0}
		_, _ = zw.CreateRaw(h)
	}
	_ = zw.Close()
	if _, err := Read(buf.Bytes()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

// A large counted-only section is tallied by a streaming scan that retains
// nothing: total allocation stays far below the section's size.
func TestReadCountsLargeSectionWithoutRetainingIt(t *testing.T) {
	const n = 8 << 20
	body := `{"likes":[` + strings.Repeat("0,", n-1) + `0],"note":"[1,2]"}`
	raw := buildZip(t, entry{name: "peertube/likes.json", body: body})

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	a, err := Read(raw)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if a.Skipped["likes"] != n {
		t.Fatalf("counted %d, want %d", a.Skipped["likes"], n)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 4<<20 {
		t.Fatalf("allocated %d bytes for a %d byte section", grew, len(body))
	}
}

func TestReadCountsNestedAndEscapedContent(t *testing.T) {
	raw := buildZip(t, entry{name: "peertube/videos.json", body: `{"videos":[{"a":[1,2,3],"s":"x]\",y"},[4,5],"z"],"other":[],"n":7}`})
	a, err := Read(raw)
	if err != nil || a.Skipped["videos"] != 3 {
		t.Fatalf("%v %+v", err, a)
	}
	if _, err := Read(buildZip(t, entry{name: "peertube/videos.json", body: `{"videos":[1,2`})); !errors.Is(err, ErrInvalid) {
		t.Fatalf("truncated json accepted: %v", err)
	}
}

func TestReadBoundsConcurrentParses(t *testing.T) {
	if cap(parseSlots) != 2 {
		t.Fatalf("parse slots = %d, want 2", cap(parseSlots))
	}
}

// countingReaderAt records how many bytes a parse actually pulled, so a test
// can assert the media region of a large archive was never read.
type countingReaderAt struct {
	r io.ReaderAt
	n int64
}

func (c *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.r.ReadAt(p, off)
	c.n += int64(n)
	return n, err
}

func TestReadAtNeverTouchesMediaEntries(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("peertube/channels.json")
	_, _ = w.Write([]byte(`{"channels":[{"name":"ok_chan"}]}`))
	// A stored 80 MiB media entry: far over MaxDeclaredTotal and MaxReadTotal.
	mw, _ := zw.CreateHeader(&zip.FileHeader{Name: "files/videos/a.mp4", Method: zip.Store})
	_, _ = mw.Write(make([]byte, 80<<20))
	// A media entry whose header lies (5 GiB declared, undecodable bytes): any
	// attempt to open it would fail or blow a cap.
	raw, _ := zw.CreateRaw(&zip.FileHeader{Name: "files/videos/b.mp4", Method: zip.Deflate, UncompressedSize64: 5 << 30, CompressedSize64: 16})
	_, _ = raw.Write(bytes.Repeat([]byte{0xFF}, 16))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	c := &countingReaderAt{r: bytes.NewReader(buf.Bytes())}
	a, err := ReadAt(c, int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Channels) != 1 {
		t.Fatalf("channels: %+v", a.Channels)
	}
	if c.n > 1<<20 {
		t.Fatalf("read %d bytes of a %d-byte archive; media must be left alone", c.n, buf.Len())
	}
}

func TestReadAtRefusesHostileDirectories(t *testing.T) {
	good := buildZip(t, entry{name: "peertube/channels.json", body: `{"channels":[]}`})
	eocd := len(good) - 22
	mut := func(f func(b []byte)) []byte { b := append([]byte(nil), good...); f(b); return b }
	for name, raw := range map[string][]byte{
		"records over cap": mut(func(b []byte) { binary.LittleEndian.PutUint16(b[eocd+10:], 0xFFFE) }),
		"offset points earlier than size allows": mut(func(b []byte) {
			binary.LittleEndian.PutUint32(b[eocd+16:], 0)
		}),
		"directory size lies": mut(func(b []byte) {
			binary.LittleEndian.PutUint32(b[eocd+12:], binary.LittleEndian.Uint32(b[eocd+12:])-1)
		}),
		"saturated sizes without zip64 record": mut(func(b []byte) { binary.LittleEndian.PutUint32(b[eocd+12:], 0xFFFFFFFF) }),
	} {
		if _, err := Read(raw); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if _, err := Read(good); err != nil {
		t.Fatalf("control archive refused: %v", err)
	}
}
