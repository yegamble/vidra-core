package peertubearchive

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
)

// Zip structure constants (APPNOTE 4.3; the same values archive/zip uses).
const (
	sigDirHeader = 0x02014b50
	sigEnd       = 0x06054b50
	sigLocator64 = 0x07064b50
	sigEnd64     = 0x06064b50
	dirHeaderLen = 46
	endLen       = 22
	locator64Len = 20
	end64Len     = 56
	// endSearchWindow is how far from EOF archive/zip looks for the end record.
	endSearchWindow = 65 * 1024
)

var sigEndBytes = []byte{'P', 'K', 5, 6}

// checkDirectory validates the zip directory BEFORE archive/zip allocates a
// File per directory record (see checkDirectoryLimits).
func checkDirectory(ra io.ReaderAt, size int64) error {
	return checkDirectoryLimits(ra, size, MaxEntries, MaxDirectoryBytes)
}

// checkDirectoryLimits locates the end-of-central-directory record the way
// archive/zip does (readDirectoryEnd / findSignatureInBlock /
// findDirectory64End in src/archive/zip/reader.go), so the record checked here
// is the record zip.NewReader will use. It is written to be stricter than EVERY
// archive/zip it may be built with, never looser, and was verified against the
// readers and writers of go1.26.2 and go1.27.2 (see verifiedZipGoMinor). Those
// two differ in ways that matter here:
//
//   - go1.26 follows a zip64 locator when records == 0xffff, size == 0xffff
//     (sic, a 16-bit test) or offset == 0xffffffff; go1.27 when records ==
//     0xffff, size == 0xffffffff or offset == 0xffffffff;
//   - go1.26's writer saturates every 32-bit end-record field once zip64 is
//     needed; go1.27's writer also emits the zip64 record and locator whenever
//     any ENTRY needed a zip64 extra, leaving the 32-bit fields at their real
//     values — so neither reader follows that locator.
//
// The rules, in terms that hold for both:
//
//   - the end record is the LAST signature in the search window with room for
//     a whole record; its comment must end exactly at EOF, and no further end
//     signature may follow it;
//   - a locator with invalid disk fields (ignored by every reader) is refused;
//   - with a valid locator, each 32-bit field must either equal its zip64
//     value or be a sentinel, and unless records or offset is a sentinel (which
//     every reader follows) all three must equal the zip64 values exactly. So
//     a reader that follows the locator and one that does not read the same
//     count, size and offset; the only difference is where the directory is
//     taken to end, which a non-following reader resolves with its base-offset
//     heuristic (directory at offset, or offset shifted by the zip64 record and
//     locator). Both starting points are walked and bounded;
//   - the directory must sit immediately before the (zip64) end record, so
//     archive/zip's base offset is 0 whenever it follows the record it reads;
//   - the directory headers are WALKED with archive/zip's termination rule
//     (stop at the first short read or non-header signature), counting records
//     and bytes. The walk continues at least as far as archive/zip's own loop,
//     so its count and size bound what zip.NewReader materialises, and both
//     must equal what the end record declares.
func checkDirectoryLimits(ra io.ReaderAt, size, maxEntries, maxDirBytes int64) error {
	if size < endLen {
		return invalid("not a zip")
	}
	n := int64(endSearchWindow)
	if n > size {
		n = size
	}
	tail := make([]byte, n)
	if err := readFullAt(ra, tail, size-n); err != nil {
		return invalid("not a zip")
	}
	pos := -1
	for i := len(tail) - endLen; i >= 0; i-- {
		if binary.LittleEndian.Uint32(tail[i:]) == sigEnd {
			pos = i
			break
		}
	}
	if pos < 0 {
		return invalid("not a zip")
	}
	rec := tail[pos:]
	if pos+endLen+int(binary.LittleEndian.Uint16(rec[20:])) != len(tail) || bytes.Contains(tail[pos+1:], sigEndBytes) {
		return invalid("ambiguous end record")
	}
	endAt := size - n + int64(pos)
	records := uint64(binary.LittleEndian.Uint16(rec[10:]))
	dirSize := uint64(binary.LittleEndian.Uint32(rec[12:]))
	dirOff := uint64(binary.LittleEndian.Uint32(rec[16:]))
	dirEnd := endAt  // where the directory must stop
	var shift uint64 // non-zero: a second start some reader may use (see above)

	if endAt >= locator64Len {
		var loc [locator64Len]byte
		if err := readFullAt(ra, loc[:], endAt-locator64Len); err != nil {
			return invalid("not a zip")
		}
		if binary.LittleEndian.Uint32(loc[:]) == sigLocator64 {
			if binary.LittleEndian.Uint32(loc[4:]) != 0 || binary.LittleEndian.Uint32(loc[16:]) != 1 {
				return invalid("inconsistent zip64 locator")
			}
			at := binary.LittleEndian.Uint64(loc[8:])
			if endAt < locator64Len+end64Len || at > uint64(endAt-locator64Len-end64Len) { //nolint:gosec // endAt >= 0
				return invalid("zip64 end record out of place")
			}
			var z [end64Len]byte
			if err := readFullAt(ra, z[:], int64(at)); err != nil || binary.LittleEndian.Uint32(z[:]) != sigEnd64 { //nolint:gosec // bounded above
				return invalid("zip64 end record missing")
			}
			records64 := binary.LittleEndian.Uint64(z[32:])
			dirSize64 := binary.LittleEndian.Uint64(z[40:])
			dirOff64 := binary.LittleEndian.Uint64(z[48:])
			allFollow := records == 0xffff || dirOff == 0xffffffff
			if allFollow {
				if (dirSize != dirSize64 && dirSize != 0xffff && dirSize != 0xffffffff) ||
					(records != records64 && records != 0xffff) || (dirOff != dirOff64 && dirOff != 0xffffffff) {
					return invalid("inconsistent zip64 locator")
				}
			} else if records != records64 || dirSize != dirSize64 || dirOff != dirOff64 {
				return invalid("inconsistent zip64 locator")
			}
			if !allFollow {
				// A reader that ignores the locator sees the directory ending at
				// the end record, computes a positive base offset and, if the first
				// header at dirOff does not parse, reads from dirOff+shift.
				shift = uint64(endAt) - at //nolint:gosec // endAt >= 0
			}
			records, dirSize, dirOff = records64, dirSize64, dirOff64
			dirEnd = int64(at) //nolint:gosec // bounded above
		}
	}
	if records > uint64(maxEntries) { //nolint:gosec // limits are positive constants
		return invalid("too many entries")
	}
	if dirSize > uint64(maxDirBytes) || dirOff > uint64(dirEnd) || dirOff+dirSize != uint64(dirEnd) { //nolint:gosec // as above
		return invalid("unsupported zip directory")
	}

	count, walked, err := walkDirectory(ra, size, dirOff, maxEntries, maxDirBytes)
	if err != nil {
		return err
	}
	if count != records || walked != dirSize {
		return invalid("zip directory does not match its end record")
	}
	if shift != 0 && dirOff+shift < uint64(size) { //nolint:gosec // size > 0
		if _, _, err := walkDirectory(ra, size, dirOff+shift, maxEntries, maxDirBytes); err != nil {
			return err
		}
	}
	return nil
}

// walkDirectory walks central-directory headers from off with archive/zip's
// termination rule and fails once the count or bytes exceed the limits.
func walkDirectory(ra io.ReaderAt, size int64, off uint64, maxEntries, maxDirBytes int64) (count, walked uint64, err error) {
	br := bufio.NewReader(io.NewSectionReader(ra, int64(off), size-int64(off))) //nolint:gosec // callers keep off < size
	for {
		var h [dirHeaderLen]byte
		if _, err := io.ReadFull(br, h[:]); err != nil || binary.LittleEndian.Uint32(h[:]) != sigDirHeader {
			return count, walked, nil
		}
		v := uint64(binary.LittleEndian.Uint16(h[28:])) + uint64(binary.LittleEndian.Uint16(h[30:])) + uint64(binary.LittleEndian.Uint16(h[32:]))
		count++
		walked += dirHeaderLen + v
		if count > uint64(maxEntries) || walked > uint64(maxDirBytes) { //nolint:gosec // limits are positive constants
			return count, walked, invalid("zip directory too large")
		}
		if _, err := br.Discard(int(v)); err != nil {
			return count, walked, nil
		}
	}
}

// readFullAt is ReadAt that tolerates io.EOF only when the buffer was filled.
func readFullAt(ra io.ReaderAt, p []byte, off int64) error {
	if off < 0 {
		return io.ErrUnexpectedEOF
	}
	n, err := ra.ReadAt(p, off)
	if n == len(p) {
		return nil
	}
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	return err
}
