package media

import (
	"path/filepath"
	"strings"
)

// AcceptedImageExts is the image-upload allowlist shared by every creator-
// supplied image on the instance — video custom thumbnails, playlist covers,
// and user/channel avatars and banners — mapping a lowercase dotted extension
// to the Content-Type served for it.
//
// The direction matters and must stay this way: the served Content-Type is
// DERIVED from the extension, never taken from the client-declared type, so a
// mislabelled upload cannot make the instance serve attacker-chosen bytes under
// an attacker-chosen type. Adding an entry here widens that surface for all
// three call sites at once, which is exactly why there is one table and not
// three copies of it.
//
// Read-only. Nothing mutates it at runtime.
var AcceptedImageExts = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".webp": "image/webp",
}

// ContentTypeForImageExt returns the Content-Type to serve for filename when its
// extension is an accepted image type, and ok=false otherwise. It is the upload
// type gate; callers reject with their own unsupported-media error.
func ContentTypeForImageExt(filename string) (contentType string, ok bool) {
	ct, ok := AcceptedImageExts[strings.ToLower(filepath.Ext(filename))]
	return ct, ok
}
