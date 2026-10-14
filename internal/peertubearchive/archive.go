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

// Limits. A metadata-only archive is a few KiB; a real export bundles the video
// files (multi-GB). The caller streams the upload to a file and hands it to
// ReadAt, which only ever opens the peertube/*.json entries: the media entries
// are validated by NAME and TYPE and then left alone, so their size is bounded
// only by the caller's upload cap, never read or decompressed here.
const (
	MaxEntries = 20000
	// MaxDirectoryBytes bounds the zip central directory. archive/zip loads the
	// whole directory into memory before we see one entry, so without this a
	// hostile multi-GB file made of empty entries is a memory bomb. 4 MiB is
	// MaxEntries at ~200 bytes a record (46-byte header + name + extra fields).
	MaxDirectoryBytes = 4 << 20
	// MaxDeclaredTotal bounds the sum of the declared uncompressed sizes of the
	// peertube/ entries only (the ones that may be read).
	MaxDeclaredTotal = 64 << 20
	MaxJSONBytes     = 8 << 20  // per entry actually read
	MaxReadTotal     = 16 << 20 // across all entries actually read
	maxNameLen       = 512

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
	// ArchiveFiles names the exported avatar/banner by path inside the zip.
	// Parsed as references only; ImageEntry resolves them to a real entry.
	ArchiveFiles struct {
		Avatar string `json:"avatar"`
		Banner string `json:"banner"`
	} `json:"archiveFiles"`
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
	// Items are the exported elements' video URLs, in order, at most
	// MaxPlaylistItems; the rest are counted in Elements only.
	Items []string
	// ChannelName is the exported owning channel's PeerTube name ("" if none).
	ChannelName string
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
	// Following holds the targetHandle (name@host) of each following.json
	// entry, at most MaxFollowing; entries past that are counted in Skipped.
	Following []string
	Skipped   map[string]int

	// files is the zip's entries by name, kept so ReadImage can open the few
	// image entries a channel references; nothing else is ever opened lazily.
	files map[string]*zip.File
}

// MaxPlaylistItems bounds how many element URLs are retained per playlist.
const MaxPlaylistItems = 1000

// MaxFollowing bounds how many following.json handles are retained.
const MaxFollowing = 5000

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

// Read parses a zip held in memory (see ReadAt).
func Read(raw []byte) (*Archive, error) {
	return ReadAt(bytes.NewReader(raw), int64(len(raw)))
}

// ReadAt parses a zip through random access (an *os.File), so a multi-GB export
// costs only its central directory and the few JSON entries. It returns
// ErrInvalid for anything hostile or malformed, and an archive with no
// recognised JSON as invalid too. The parse slot is held only here, never while
// the caller is receiving the upload.
func ReadAt(ra io.ReaderAt, size int64) (*Archive, error) {
	parseSlots <- struct{}{}
	defer func() { <-parseSlots }()
	if err := checkDirectory(ra, size); err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(ra, size)
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
		// Only the entries we may read count toward the declared total; a media
		// entry's declared size is never acted on.
		if strings.HasPrefix(f.Name, "peertube/") {
			declared += f.UncompressedSize64
			if declared > MaxDeclaredTotal {
				return nil, invalid("declared size too large")
			}
		}
		files[f.Name] = f
	}

	a := &Archive{Skipped: map[string]int{}, files: files}
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
				DisplayName string `json:"displayName"`
				Description string `json:"description"`
				Privacy     int    `json:"privacy"`
				Type        int    `json:"type"`
				Elements    []struct {
					VideoURL string `json:"videoUrl"`
				} `json:"elements"`
				Channel *struct {
					Name string `json:"name"`
				} `json:"channel"`
			} `json:"videoPlaylists"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			return nil, invalid("video-playlists.json is not valid")
		}
		for _, p := range doc.Playlists {
			pl := Playlist{DisplayName: p.DisplayName, Description: p.Description, Privacy: p.Privacy, Type: p.Type, Elements: len(p.Elements)}
			for _, e := range p.Elements {
				if u := strings.TrimSpace(e.VideoURL); len(pl.Items) < MaxPlaylistItems && len(u) <= 2048 {
					pl.Items = append(pl.Items, u)
				}
			}
			if p.Channel != nil {
				pl.ChannelName = strings.TrimSpace(p.Channel.Name)
			}
			a.Playlists = append(a.Playlists, pl)
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
	parsedFollowing := false
	if f, ok := files["peertube/following.json"]; ok && f.UncompressedSize64 <= MaxJSONBytes {
		b, err := readEntry(f, &budget)
		if err != nil {
			return nil, err
		}
		var doc struct {
			Following []struct {
				TargetHandle string `json:"targetHandle"`
			} `json:"following"`
		}
		if json.Unmarshal(b, &doc) == nil {
			parsedFollowing = true
			for _, e := range doc.Following {
				if h := strings.TrimSpace(e.TargetHandle); h != "" && len(a.Following) < MaxFollowing {
					a.Following = append(a.Following, h)
				} else {
					a.Skipped["following"]++ // unusable entry or past the retention cap
				}
			}
		}
	}
	var streamBudget int64 = MaxStreamBytes
	for file, section := range skipSections {
		f, ok := files["peertube/"+file]
		if !ok || (parsedFollowing && section == "following") {
			continue
		}
		n, err := countSection(f, &streamBudget)
		if err != nil {
			return nil, invalid("%s is not valid", file)
		}
		a.Skipped[section] += n
	}
	if a.Channels == nil && a.Playlists == nil && a.Settings == nil && len(a.Following) == 0 && len(a.Skipped) == 0 {
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
