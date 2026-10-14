package peertubearchive

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"testing"
)

// goDirectoryCost is what archive/zip actually materialised for a directory:
// the record count and the central-directory bytes it parsed.
func goDirectoryCost(raw []byte) (entries, dirBytes int, ok bool) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return 0, 0, false
	}
	for _, f := range zr.File {
		dirBytes += 46 + len(f.Name) + len(f.Extra) + len(f.Comment)
	}
	return len(zr.File), dirBytes, true
}

// assertAgreesWithArchiveZip is the differential property: whenever the
// pre-check accepts, archive/zip's own view of the directory is within the
// same limits the pre-check enforced.
func assertAgreesWithArchiveZip(t *testing.T, raw []byte, maxEntries, maxDirBytes int64) {
	t.Helper()
	if checkDirectoryLimits(bytes.NewReader(raw), int64(len(raw)), maxEntries, maxDirBytes) != nil {
		return
	}
	n, b, ok := goDirectoryCost(raw)
	if !ok {
		return
	}
	if int64(n) > maxEntries || int64(b) > maxDirBytes {
		t.Fatalf("pre-check accepted, archive/zip parsed %d entries / %d directory bytes (limits %d / %d)", n, b, maxEntries, maxDirBytes)
	}
}

// eocdOf returns the offset of the end record of a writer-produced zip (no comment).
func eocdOf(raw []byte) int { return len(raw) - 22 }

// withZip64 rebuilds a writer-produced zip with a zip64 end record and locator
// in front of its end record. mutate edits the 32-bit end record, loc the
// locator, rec the zip64 record, before they are joined.
func withZip64(good []byte, mutate func(eocd []byte), rec func(r []byte), loc func(l []byte)) []byte {
	e := eocdOf(good)
	records := uint64(binary.LittleEndian.Uint16(good[e+10:]))
	dirSize := uint64(binary.LittleEndian.Uint32(good[e+12:]))
	dirOff := uint64(binary.LittleEndian.Uint32(good[e+16:]))

	r := make([]byte, 56)
	binary.LittleEndian.PutUint32(r[0:], 0x06064b50)
	binary.LittleEndian.PutUint64(r[4:], 44)
	binary.LittleEndian.PutUint16(r[12:], 45)
	binary.LittleEndian.PutUint16(r[14:], 45)
	binary.LittleEndian.PutUint64(r[24:], records)
	binary.LittleEndian.PutUint64(r[32:], records)
	binary.LittleEndian.PutUint64(r[40:], dirSize)
	binary.LittleEndian.PutUint64(r[48:], dirOff)
	if rec != nil {
		rec(r)
	}
	l := make([]byte, 20)
	binary.LittleEndian.PutUint32(l[0:], 0x07064b50)
	binary.LittleEndian.PutUint64(l[8:], uint64(e))
	binary.LittleEndian.PutUint32(l[16:], 1)
	if loc != nil {
		loc(l)
	}
	end := append([]byte(nil), good[e:]...)
	binary.LittleEndian.PutUint16(end[10:], 0xffff)
	binary.LittleEndian.PutUint16(end[8:], 0xffff)
	binary.LittleEndian.PutUint32(end[12:], 0xffffffff)
	binary.LittleEndian.PutUint32(end[16:], 0xffffffff)
	if mutate != nil {
		mutate(end)
	}
	out := append(append(append(append([]byte(nil), good[:e]...), r...), l...), end...)
	return out
}

// craftedDirectories is the seed set for the differential test and the fuzz target.
func craftedDirectories(t testing.TB) map[string][]byte {
	good := buildZipTB(t, entry{name: "peertube/channels.json", body: `{"channels":[]}`}, entry{name: "files/a.mp4", body: "x"})
	e := eocdOf(good)
	mut := func(f func(b []byte) []byte) []byte { return f(append([]byte(nil), good...)) }
	end := append([]byte(nil), good[e:]...)

	one := buildZipTB(t, entry{name: "a", body: "x"})
	oe := eocdOf(one)
	return map[string][]byte{
		"good": good,
		// The only directory header claims a comment that swallows the end
		// record and its (padded) comment: archive/zip accepts it and parses far
		// more directory bytes than the end record declares.
		"header comment swallows end record": func() []byte {
			b := append([]byte(nil), one...)
			const c = 300
			off := int(binary.LittleEndian.Uint32(b[oe+16:]))
			binary.LittleEndian.PutUint16(b[off+32:], endLen+c-4)
			binary.LittleEndian.PutUint16(b[oe+20:], c)
			return append(b, make([]byte, c)...)
		}(),
		// A second end record inside the first one's comment, followed by one
		// trailing byte. archive/zip uses the LAST signature whose comment fits.
		"two end records, trailing byte": mut(func(b []byte) []byte {
			binary.LittleEndian.PutUint16(b[e+20:], 22+1)
			return append(append(b, end...), 0)
		}),
		"two end records, exact": mut(func(b []byte) []byte {
			binary.LittleEndian.PutUint16(b[e+20:], 22)
			return append(b, end...)
		}),
		"comment shorter than the tail": mut(func(b []byte) []byte {
			return append(b, 'x', 'y')
		}),
		"signature in the last bytes": mut(func(b []byte) []byte {
			binary.LittleEndian.PutUint16(b[e+20:], 6)
			return append(b, 'P', 'K', 5, 6, 0, 0)
		}),
		"zip64 all sentinels": withZip64(good, nil, nil, nil),
		"zip64 size sentinel 0xffff only, records over cap": withZip64(good, func(end []byte) {
			binary.LittleEndian.PutUint16(end[8:], 2)
			binary.LittleEndian.PutUint16(end[10:], 2)
			binary.LittleEndian.PutUint32(end[12:], 0xffff)
			binary.LittleEndian.PutUint32(end[16:], uint32(binary.LittleEndian.Uint32(good[e+16:])))
		}, func(r []byte) { binary.LittleEndian.PutUint64(r[32:], MaxEntries+1) }, nil),
		// The shape go1.27's zip.Writer emits once any entry carries a zip64
		// extra: a locator and zip64 record, 32-bit fields left at real values.
		"zip64 locator without sentinels": withZip64(good, func(end []byte) { copy(end[8:20], good[e+8:e+20]) }, nil, nil),
		"zip64 locator without sentinels, zip64 records disagree": withZip64(good, func(end []byte) { copy(end[8:20], good[e+8:e+20]) },
			func(r []byte) { binary.LittleEndian.PutUint64(r[32:], 3) }, nil),
		"zip64 size 0xffff only, zip64 size disagrees": withZip64(good, func(end []byte) {
			copy(end[8:20], good[e+8:e+20])
			binary.LittleEndian.PutUint32(end[12:], 0xffff)
		}, nil, nil),
		"zip64 size 0xffffffff only (not a sentinel for archive/zip)": withZip64(good, func(end []byte) {
			copy(end[8:20], good[e+8:e+20])
			binary.LittleEndian.PutUint32(end[12:], 0xffffffff)
		}, nil, nil),
		"zip64 locator with bad disk count": withZip64(good, nil, nil, func(l []byte) { binary.LittleEndian.PutUint32(l[16:], 2) }),
		"zip64 records over cap":            withZip64(good, nil, func(r []byte) { binary.LittleEndian.PutUint64(r[32:], 1<<40) }, nil),
		"zip64 record offset out of file":   withZip64(good, nil, nil, func(l []byte) { binary.LittleEndian.PutUint64(l[8:], 1<<62) }),
		"zip64 record offset negative":      withZip64(good, nil, nil, func(l []byte) { binary.LittleEndian.PutUint64(l[8:], 1<<63) }),
		"records over cap": mut(func(b []byte) []byte {
			binary.LittleEndian.PutUint16(b[e+10:], 0xfffe)
			return b
		}),
		"directory offset zero": mut(func(b []byte) []byte {
			binary.LittleEndian.PutUint32(b[e+16:], 0)
			return b
		}),
		"directory size short by one": mut(func(b []byte) []byte {
			binary.LittleEndian.PutUint32(b[e+12:], binary.LittleEndian.Uint32(b[e+12:])-1)
			return b
		}),
		// The last directory header claims a comment running into the end
		// record, so archive/zip would keep reading headers past the declared size.
		"header runs past declared directory": mut(func(b []byte) []byte {
			off := int(binary.LittleEndian.Uint32(b[e+16:]))
			n1 := int(binary.LittleEndian.Uint16(b[off+28:])) + int(binary.LittleEndian.Uint16(b[off+30:])) + int(binary.LittleEndian.Uint16(b[off+32:]))
			second := off + 46 + n1
			binary.LittleEndian.PutUint16(b[second+32:], 22)
			return b
		}),
	}
}

// buildZipTB is buildZip for testing.TB (fuzz seeds are built from *testing.F).
func buildZipTB(t testing.TB, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Deflate})
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

func TestCheckDirectorySelectsTheRecordArchiveZipUses(t *testing.T) {
	cases := craftedDirectories(t)
	accept := map[string]bool{"good": true, "zip64 all sentinels": true, "zip64 locator without sentinels": true}
	for name, raw := range cases {
		err := checkDirectory(bytes.NewReader(raw), int64(len(raw)))
		if accept[name] {
			if err != nil {
				t.Errorf("%s: refused: %v", name, err)
			}
			if _, _, ok := goDirectoryCost(raw); !ok {
				t.Errorf("%s: control case is not readable by archive/zip", name)
			}
		} else if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
		assertAgreesWithArchiveZip(t, raw, MaxEntries, MaxDirectoryBytes)
	}
}

// The differential property under tiny limits, so the crafted cases (and the
// fuzzer) can actually reach them: a directory just over the limits, padded
// with extra fields and comments that stretch each header.
func TestCheckDirectoryDifferentialSmallLimits(t *testing.T) {
	for n := 1; n <= 6; n++ {
		var entries []entry
		for i := 0; i < n; i++ {
			entries = append(entries, entry{name: fmt.Sprintf("peertube/%d.json", i), body: "{}"})
		}
		raw := buildZipTB(t, entries...)
		for _, lim := range [][2]int64{{3, 1 << 20}, {100, 150}, {100, 300}} {
			assertAgreesWithArchiveZip(t, raw, lim[0], lim[1])
			err := checkDirectoryLimits(bytes.NewReader(raw), int64(len(raw)), lim[0], lim[1])
			cost := int64(0)
			for i := 0; i < n; i++ {
				cost += 46 + int64(len(fmt.Sprintf("peertube/%d.json", i)))
			}
			if want := int64(n) <= lim[0] && cost <= lim[1]; (err == nil) != want {
				t.Errorf("n=%d lim=%v: err=%v, want accept=%v", n, lim, err, want)
			}
		}
	}
	for _, raw := range craftedDirectories(t) {
		assertAgreesWithArchiveZip(t, raw, 1, 60)
		assertAgreesWithArchiveZip(t, raw, 2, 200)
	}
}

func FuzzCheckDirectoryAgreesWithArchiveZip(f *testing.F) {
	for _, raw := range craftedDirectories(f) {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		assertAgreesWithArchiveZip(t, raw, MaxEntries, MaxDirectoryBytes)
		assertAgreesWithArchiveZip(t, raw, 1, 60)
		assertAgreesWithArchiveZip(t, raw, 2, 120)
	})
}

// The zip64 shapes the runtime's own zip.Writer produces must be accepted,
// whatever toolchain builds this: an entry whose declared size needs a zip64
// extra (go1.27 then writes a locator without end-record sentinels).
func TestCheckDirectoryAcceptsRuntimeZip64Writer(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("peertube/channels.json")
	_, _ = w.Write([]byte(`{}`))
	raw, err := zw.CreateRaw(&zip.FileHeader{Name: "files/b.mp4", Method: zip.Deflate, UncompressedSize64: 5 << 30, CompressedSize64: 16})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = raw.Write(bytes.Repeat([]byte{0xFF}, 16))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	if err := checkDirectory(bytes.NewReader(b), int64(len(b))); err != nil {
		t.Fatalf("%s: runtime-written zip64 archive refused: %v", runtime.Version(), err)
	}
	if n, _, ok := goDirectoryCost(b); !ok || n != 2 {
		t.Fatalf("archive/zip read %d entries (ok=%v)", n, ok)
	}
}

// verifiedZipGoMinor is the newest Go minor whose src/archive/zip reader and
// writer checkDirectoryLimits was verified against (go1.26.2 and go1.27.2:
// differential + fuzz tests run under both). A newer toolchain fails here
// until someone diffs src/archive/zip/{reader,writer}.go against the last
// verified release, re-runs FuzzCheckDirectoryAgreesWithArchiveZip on it, and
// bumps this constant.
const verifiedZipGoMinor = 27

var goMinorRE = regexp.MustCompile(`go1\.(\d+)`)

func TestToolchainZipReaderWasVerified(t *testing.T) {
	v := runtime.Version()
	m := goMinorRE.FindStringSubmatch(v)
	if m == nil {
		t.Fatalf("cannot parse Go version %q: re-verify checkDirectoryLimits against this toolchain's archive/zip", v)
	}
	minor, _ := strconv.Atoi(m[1])
	if minor > verifiedZipGoMinor {
		t.Fatalf("%s is newer than the newest Go minor checkDirectoryLimits was verified against (1.%d): diff src/archive/zip/{reader,writer}.go, re-run the fuzz target, then bump verifiedZipGoMinor", v, verifiedZipGoMinor)
	}
}
