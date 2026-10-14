// Package peertubearchive reads a PeerTube user-export archive (>= 6.1: a zip
// whose peertube/*.json files describe the account) into plain values.
//
// The archive is attacker-supplied, so the reader is deliberately small and
// defensive: it never extracts to disk, never follows an entry name anywhere,
// and reads only the handful of JSON entries it understands, each through a
// hard byte cap. Anything structurally hostile (path traversal, symlinks,
// encrypted or duplicate entries, too many entries, an absurd declared size)
// rejects the WHOLE archive rather than being skipped, so a file that merely
// looks fine to a scanner that stops at the first entry cannot smuggle a second
// meaning past us.
package peertubearchive

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
)

// Limits. A metadata-only archive is a few KiB; real exports that bundle video
// files are far larger and are refused by the caller's body limit before they
// reach this package.
const (
	MaxEntries       = 2000
	MaxDeclaredTotal = 64 << 20 // sum of every entry's declared uncompressed size
	MaxJSONBytes     = 8 << 20  // per entry actually read
	MaxReadTotal     = 16 << 20 // across all entries actually read
	maxNameLen       = 512

	// MaxUploadBytes bounds the zip itself (the default HTTP body limit).
	MaxUploadBytes = 8 << 20

	// MaxStreamBytes bounds the decompressed bytes scanned for counted-only
	// sections, per section and in total. They are scanned, never buffered.
	MaxStreamBytes = 32 << 20
)

// ErrInvalid wraps every reason an archive is refused.
var ErrInvalid = errors.New("peertubearchive: invalid archive")

// Channel is one exported channel (PeerTube's actor `name` is its handle).
type Channel struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
}

// Playlist is one exported playlist. Privacy: 1 public, 2 unlisted, 3 private.
// Type: 1 regular, 2 watch-later. Elements is the item count only: items
// reference videos by URL, which slice 1 does not resolve.
type Playlist struct {
	DisplayName string
	Description string
	Privacy     int
	Type        int
	Elements    int
}

// Settings holds the user-settings fields that map 1:1 onto vidra settings.
type Settings struct {
	AutoPlayNextVideo *bool
}

// Archive is everything the reader understood plus counts of what it saw but
// does not import, keyed by section.
type Archive struct {
	Channels  []Channel
	Playlists []Playlist
	Settings  *Settings
	Skipped   map[string]int
}

// skipSections maps a peertube/<file> to the section name it is reported under.
var skipSections = map[string]string{
	"videos.json": "videos", "comments.json": "comments", "likes.json": "likes",
	"dislikes.json": "dislikes", "video-history.json": "watch_history",
	"blocklist.json": "blocklist", "follower.json": "followers",
	"following.json": "following", "watched-words-lists.json": "watched_words",
	"automatic-tag-policies.json": "automatic_tag_policies",
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// parseSlots bounds how many archives are parsed at once process-wide, so the
// CPU spent on uploads stays flat however many users upload together.
var parseSlots = make(chan struct{}, 2)

// Read parses a zip held in memory. It returns ErrInvalid for anything
// hostile or malformed, and an archive with no recognised JSON as invalid too.
func Read(raw []byte) (*Archive, error) {
	parseSlots <- struct{}{}
	defer func() { <-parseSlots }()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, invalid("not a zip")
	}
	if len(zr.File) > MaxEntries {
		return nil, invalid("too many entries")
	}
	seen := map[string]bool{}
	var declared uint64
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		if err := checkEntry(f); err != nil {
			return nil, err
		}
		key := strings.ToLower(strings.TrimSuffix(f.Name, "/"))
		if seen[key] {
			return nil, invalid("duplicate entry")
		}
		seen[key] = true
		declared += f.UncompressedSize64
		if declared > MaxDeclaredTotal {
			return nil, invalid("declared size too large")
		}
		files[f.Name] = f
	}

	a := &Archive{Skipped: map[string]int{}}
	var budget int64 = MaxReadTotal
	read := func(name string) ([]byte, error) {
		f, ok := files["peertube/"+name]
		if !ok {
			return nil, nil
		}
		b, err := readEntry(f, &budget)
		if err != nil {
			return nil, err
		}
		return b, nil
	}

	if b, err := read("channels.json"); err != nil {
		return nil, err
	} else if b != nil {
		var doc struct {
			Channels []Channel `json:"channels"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			return nil, invalid("channels.json is not valid")
		}
		a.Channels = doc.Channels
	}
	if b, err := read("video-playlists.json"); err != nil {
		return nil, err
	} else if b != nil {
		var doc struct {
			Playlists []struct {
				DisplayName string     `json:"displayName"`
				Description string     `json:"description"`
				Privacy     int        `json:"privacy"`
				Type        int        `json:"type"`
				Elements    []struct{} `json:"elements"`
			} `json:"videoPlaylists"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			return nil, invalid("video-playlists.json is not valid")
		}
		for _, p := range doc.Playlists {
			a.Playlists = append(a.Playlists, Playlist{p.DisplayName, p.Description, p.Privacy, p.Type, len(p.Elements)})
		}
	}
	if b, err := read("user-settings.json"); err != nil {
		return nil, err
	} else if b != nil {
		var s struct {
			AutoPlayNextVideo *bool `json:"autoPlayNextVideo"`
		}
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, invalid("user-settings.json is not valid")
		}
		a.Settings = &Settings{AutoPlayNextVideo: s.AutoPlayNextVideo}
	}
	var streamBudget int64 = MaxStreamBytes
	for file, section := range skipSections {
		f, ok := files["peertube/"+file]
		if !ok {
			continue
		}
		n, err := countSection(f, &streamBudget)
		if err != nil {
			return nil, invalid("%s is not valid", file)
		}
		a.Skipped[section] += n
	}
	if a.Channels == nil && a.Playlists == nil && a.Settings == nil && len(a.Skipped) == 0 {
		return nil, invalid("no peertube export data found")
	}
	return a, nil
}

// checkEntry refuses a hostile entry name or type. Names are never used to
// touch the filesystem here, but the rule is enforced anyway so a future
// extractor cannot inherit an archive this package called clean.
func checkEntry(f *zip.File) error {
	n := f.Name
	if n == "" || len(n) > maxNameLen || strings.ContainsAny(n, "\x00\\:") || strings.HasPrefix(n, "/") {
		return invalid("unsafe entry name")
	}
	for _, seg := range strings.Split(strings.TrimSuffix(n, "/"), "/") {
		if seg == ".." || seg == "." || seg == "" {
			return invalid("unsafe entry name")
		}
	}
	if m := f.Mode(); m&fs.ModeSymlink != 0 || (!m.IsRegular() && !m.IsDir()) {
		return invalid("non-regular entry")
	}
	if f.Flags&0x1 != 0 {
		return invalid("encrypted entry")
	}
	return nil
}

// readEntry reads one entry through per-entry and archive-wide caps. The
// declared size is attacker-controlled, so the cap is enforced on the bytes
// actually produced, not trusted from the header.
func readEntry(f *zip.File, budget *int64) ([]byte, error) {
	if f.UncompressedSize64 > MaxJSONBytes {
		return nil, invalid("entry too large")
	}
	rc, err := f.Open()
	if err != nil {
		return nil, invalid("unreadable entry")
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(io.LimitReader(rc, MaxJSONBytes+1))
	if err != nil || len(b) > MaxJSONBytes {
		return nil, invalid("entry unreadable or too large")
	}
	if *budget -= int64(len(b)); *budget < 0 {
		return nil, invalid("archive expands too far")
	}
	return b, nil
}

// countSection counts the elements of every array that is a direct value of
// the entry's top-level object, by scanning bytes. Nothing is retained, so
// memory is constant however large the section is. The scan is capped by
// MaxStreamBytes and refuses input that is not a well-formed object.
func countSection(f *zip.File, budget *int64) (int, error) {
	if f.UncompressedSize64 > MaxStreamBytes {
		return 0, ErrInvalid
	}
	rc, err := f.Open()
	if err != nil {
		return 0, ErrInvalid
	}
	defer func() { _ = rc.Close() }()
	br := bufio.NewReaderSize(io.LimitReader(rc, MaxStreamBytes+1), 32<<10)

	var (
		depth, count int
		inStr, esc   bool
		inArr        bool // directly inside an array that is a top-level value
		sawElem      bool
		started      bool
		read         int64
	)
	for {
		c, err := br.ReadByte()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, ErrInvalid
		}
		if read++; read > MaxStreamBytes {
			return 0, ErrInvalid
		}
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		if !started {
			if c == ' ' || c == '\n' || c == '\r' || c == '\t' {
				continue
			}
			if c != '{' {
				return 0, ErrInvalid
			}
			started = true
		}
		switch c {
		case '"':
			inStr = true
			if inArr && depth == 2 {
				sawElem = true
			}
		case '{', '[':
			depth++
			if depth == 2 && c == '[' {
				inArr, sawElem = true, false
			} else if inArr && depth == 3 {
				sawElem = true
			}
		case '}', ']':
			if depth--; depth < 0 {
				return 0, ErrInvalid
			}
			if depth == 1 && inArr {
				if sawElem {
					count++
				}
				inArr = false
			}
		case ',':
			if inArr && depth == 2 {
				count++
				sawElem = false
			}
		case ' ', '\n', '\r', '\t', ':':
		default:
			if inArr && depth == 2 {
				sawElem = true
			}
		}
		if depth == 0 && started && c == '}' {
			break
		}
	}
	if !started || depth != 0 || inStr {
		return 0, ErrInvalid
	}
	if *budget -= read; *budget < 0 {
		return 0, ErrInvalid
	}
	return count, nil
}
