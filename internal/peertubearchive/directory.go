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

// checkDirectoryLimits locates the end-of-central-directory record exactly as
// archive/zip does (readDirectoryEnd / findSignatureInBlock /
// findDirectory64End in the toolchain's src/archive/zip/reader.go), so the
// record checked here is the record zip.NewReader will use. Where archive/zip
// is lenient this is stricter, never looser:
//
//   - the end record is the LAST signature in the search window with room for
//     a whole record; its comment must end exactly at EOF, and no further end
//     signature may follow it;
//   - archive/zip consults a zip64 locator only when a 32-bit field holds its
//     sentinel (records 0xffff, size 0xffff, offset 0xffffffff — sic, the size
//     test is 16-bit there). A locator it would ignore, or one it would follow
//     with invalid disk fields, is a second description of the directory and is
//     refused; a followed locator is authoritative;
//   - the directory must sit immediately before the (zip64) end record, so
//     archive/zip's base offset is 0;
//   - the directory headers are then WALKED with archive/zip's termination
//     rule (stop at the first short read or non-header signature), counting
//     records and bytes. The walk continues at least as far as archive/zip's
//     own loop, so its count and size bound what zip.NewReader materialises,
//     and both must equal what the end record declares.
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
	dirEnd := endAt // where the directory must stop

	if endAt >= locator64Len {
		var loc [locator64Len]byte
		if err := readFullAt(ra, loc[:], endAt-locator64Len); err != nil {
			return invalid("not a zip")
		}
		if binary.LittleEndian.Uint32(loc[:]) == sigLocator64 {
			follows := records == 0xffff || dirSize == 0xffff || dirOff == 0xffffffff
			validDisks := binary.LittleEndian.Uint32(loc[4:]) == 0 && binary.LittleEndian.Uint32(loc[16:]) == 1
			if !follows || !validDisks {
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
			records = binary.LittleEndian.Uint64(z[32:])
			dirSize = binary.LittleEndian.Uint64(z[40:])
			dirOff = binary.LittleEndian.Uint64(z[48:])
			dirEnd = int64(at) //nolint:gosec // bounded above
		}
	}
	if records > uint64(maxEntries) { //nolint:gosec // limits are positive constants
		return invalid("too many entries")
	}
	if dirSize > uint64(maxDirBytes) || dirOff > uint64(dirEnd) || dirOff+dirSize != uint64(dirEnd) { //nolint:gosec // as above
		return invalid("unsupported zip directory")
	}

	br := bufio.NewReader(io.NewSectionReader(ra, int64(dirOff), size-int64(dirOff))) //nolint:gosec // dirOff <= dirEnd < size
	var count, walked uint64
	for {
		var h [dirHeaderLen]byte
		if _, err := io.ReadFull(br, h[:]); err != nil || binary.LittleEndian.Uint32(h[:]) != sigDirHeader {
			break
		}
		v := uint64(binary.LittleEndian.Uint16(h[28:])) + uint64(binary.LittleEndian.Uint16(h[30:])) + uint64(binary.LittleEndian.Uint16(h[32:]))
		count++
		walked += dirHeaderLen + v
		if count > uint64(maxEntries) || walked > uint64(maxDirBytes) { //nolint:gosec // as above
			return invalid("zip directory too large")
		}
		if _, err := br.Discard(int(v)); err != nil {
			break
		}
	}
	if count != records || walked != dirSize {
		return invalid("zip directory does not match its end record")
	}
	return nil
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
