package storage

import (
	"path"
	"strings"
)

// Content types for stored media, derived from the object KEY's extension.
//
// WHY THIS EXISTS, AND WHY HERE. A stored object carries whatever Content-Type
// the store recorded at PUT time, and until this file that was
// `application/octet-stream` for every object Vidra writes — the S3 PUT set
// none, so minio-go's default won. The API proxy hid it: serveStoredObjectNamed
// sets the type from the DATABASE and, failing that, http.ServeContent sniffs
// the first 512 bytes. Nothing that reads the object DIRECTLY can do either.
// A presigned redirect can only pin headers it is told to pin, and a CDN edge —
// a third party pulling from the bucket at the object's own key — cannot be
// told anything at all: it forwards what the origin returns. So an object with
// no stored type is delivered as `application/octet-stream` by every path that
// is not the proxy, which is the one delivery path this table is not for.
//
// The table is EXPLICIT rather than mime.TypeByExtension because that function
// consults the host's /etc/mime.types and Windows registry: the same key would
// get different answers on a developer's laptop, in CI and in the release
// image, and "what type is this object served as" must not be a property of the
// machine that uploaded it.
//
// Extensions absent from the table return "", which means "say nothing" — the
// caller then behaves exactly as it did before this file existed. That is the
// safe direction: a wrong Content-Type is worse than none, and the set below is
// closed to what Vidra actually writes (internal/media's key grammar) plus the
// image types media.AcceptedImageExts admits.
var storedContentTypes = map[string]string{
	// Progressive and whole-file video.
	".mp4":  "video/mp4",
	".webm": "video/webm",
	".mov":  "video/quicktime",
	// HLS/CMAF media objects. `.m4s` and init `.mp4` are both video/mp4 —
	// the same value the API proxy serves them with (media.HLSMP4ContentType),
	// deliberately NOT `video/iso.segment`, which no browser asks for.
	".m4s": "video/mp4",
	".ts":  "video/mp2t",
	// Audio-only download.
	".m4a": "audio/mp4",
	".mp3": "audio/mpeg",
	// Manifests. Neither is ever redirected (delivery.Redirectable is false for
	// an HLS playlist), but they are read directly by mediagc and by an
	// operator with a bucket browser, and an m3u8 served as octet-stream is a
	// download prompt rather than a manifest.
	".m3u8": "application/vnd.apple.mpegurl",
	".mpd":  "application/dash+xml",
	// Images: posters, storyboard sprites, avatars, banners, playlist covers.
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".webp": "image/webp",
	// Text tracks. The charset is part of the value because a VTT without one
	// is decoded as the reader's default and non-ASCII captions mojibake.
	".vtt": "text/vtt; charset=utf-8",
}

// ContentTypeForKey returns the Content-Type an object stored at key should be
// served with, or "" when the extension is not one Vidra writes.
//
// It is derived from the key and NEVER from client-declared data, for the same
// reason media.ContentTypeForImageExt is: a mislabelled upload must not be able
// to make the instance serve attacker-chosen bytes under an attacker-chosen
// type. Keys are minted by internal/media, so their extensions are the
// instance's own vocabulary.
func ContentTypeForKey(key string) string {
	ext := strings.ToLower(path.Ext(key))
	if ext == "" {
		return ""
	}
	return storedContentTypes[ext]
}
