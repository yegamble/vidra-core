package blobverify

import (
	"errors"
	"net/url"
	"path"
	"strings"
	"unicode"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/mediagc"
	"github.com/vidra/vidra-core/internal/mediaroute"
)

// resolveHLSReference never fetches a URL. It confines a stored reference to
// the promoted master's exact tree and the child shapes its HTTP route serves.
func resolveHLSReference(pl mediagc.PlaylistRef, parent, raw string) (string, error) {
	invalid := errors.New("unservable HLS dependency reference")
	if pl.Format != media.HLSFormatTS && pl.Format != media.HLSFormatCMAF {
		return "", errors.New("unsupported recorded HLS format")
	}
	canonical := func(s string) bool {
		return s != "" && s != ".." && !strings.HasPrefix(s, "../") && !strings.HasPrefix(s, "/") && path.Clean(s) == s &&
			!strings.ContainsAny(s, "\\%?#") && strings.IndexFunc(s, unicode.IsControl) < 0
	}
	prefix := path.Dir(pl.MasterKey) + "/"
	if !canonical(pl.MasterKey) || prefix == "./" || !canonical(parent) || !strings.HasPrefix(parent, prefix) {
		return "", invalid
	}
	// Escapes have several interpretations across URL routers and storage
	// backends. Stored native/imported file names need no percent decoding.
	if raw == "" || strings.ContainsAny(raw, "\\%?#") || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return "", invalid
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || strings.HasPrefix(u.Path, "/") {
		return "", invalid
	}
	key := path.Join(path.Dir(parent), u.Path)
	if !strings.HasPrefix(key, prefix) {
		return "", invalid
	}
	rel := strings.TrimPrefix(key, prefix)
	peertube := mediaroute.IsPeerTubeHLSMasterKey(pl.MasterKey)
	if !mediaroute.HLSServedRel(rel, pl.MasterKey, peertube) {
		return "", invalid
	}
	if !peertube && rel != path.Base(pl.MasterKey) {
		rendition, file, _ := strings.Cut(rel, "/")
		_, cmaf, _ := mediaroute.HLSChildShape(rendition, file)
		if cmaf != (pl.Format == media.HLSFormatCMAF) {
			return "", invalid
		}
	}
	return key, nil
}
