package hlsmanifest

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"unicode/utf8"
)

// MaxPlaylistBytes is the ceiling used by the HLS serving path.
const MaxPlaylistBytes = 16 << 20

// ReferenceKind identifies the required role of a playlist dependency.
type ReferenceKind string

const (
	PlaylistReference  ReferenceKind = "playlist"
	MapReference       ReferenceKind = "map"
	KeyReference       ReferenceKind = "key"
	MediaReference     ReferenceKind = "media"
	AuxiliaryReference ReferenceKind = "auxiliary"
)

// Reference is an immediate dependency, not an assertion about its descendants.
type Reference struct {
	URI  string
	Kind ReferenceKind
	Line int
}

// VisitReferences yields dependencies without building an unbounded queue. The
// caller confines URI resolution and can refuse each reference before retaining
// it. A missing child playlist cannot reveal its own dependencies here.
func VisitReferences(body []byte, visit func(Reference) error) error {
	invalid := errors.New("invalid HLS dependency manifest")
	if visit == nil || len(body) > MaxPlaylistBytes || !utf8.Valid(body) || bytes.IndexByte(body, 0) >= 0 {
		return invalid
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 4096), MaxPlaylistBytes+1)
	if !scanner.Scan() || scanner.Text() != "#EXTM3U" {
		return invalid
	}
	lineNumber, pending := 1, false
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		kind := MediaReference
		if !strings.HasPrefix(line, "#") {
			if pending {
				kind, pending = PlaylistReference, false
			}
			if err := visit(Reference{URI: line, Kind: kind, Line: lineNumber}); err != nil {
				return err
			}
			continue
		}
		tag, list, _ := strings.Cut(line, ":")
		required := false
		switch tag {
		case "#EXT-X-STREAM-INF":
			if pending {
				return invalid
			}
			pending = true
		case "#EXT-X-MEDIA", "#EXT-X-I-FRAME-STREAM-INF":
			kind, required = PlaylistReference, tag == "#EXT-X-I-FRAME-STREAM-INF"
		case "#EXT-X-MAP":
			kind, required = MapReference, true
		case "#EXT-X-KEY", "#EXT-X-SESSION-KEY":
			kind, required = KeyReference, true
		default:
			kind = AuxiliaryReference
			if !strings.Contains(list, "URI=") {
				continue
			}
		}
		attributes, err := ParseAttributes(list)
		if err != nil {
			return err
		}
		values := make(map[string]Attribute, len(attributes))
		for _, attribute := range attributes {
			values[attribute.Name] = attribute
		}
		if tag == "#EXT-X-MEDIA" {
			mediaType := values["TYPE"]
			if mediaType.Value == "" || mediaType.Quoted {
				return invalid
			}
			required = mediaType.Value == "SUBTITLES"
		}
		if kind == KeyReference {
			method := values["METHOD"]
			if method.Value == "" || method.Quoted || (method.Value == "NONE" && tag == "#EXT-X-SESSION-KEY") {
				return invalid
			}
			if method.Value == "NONE" {
				if len(attributes) != 1 {
					return invalid
				}
				continue
			}
		}
		if required {
			uri := values["URI"]
			if !uri.Quoted || uri.Value == "" {
				return invalid
			}
		}
		for _, attribute := range attributes {
			if !strings.HasSuffix(attribute.Name, "URI") {
				continue
			}
			if !attribute.Quoted || attribute.Value == "" {
				return invalid
			}
			if err := visit(Reference{URI: attribute.Value, Kind: kind, Line: lineNumber}); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if pending {
		return invalid
	}
	return nil
}
