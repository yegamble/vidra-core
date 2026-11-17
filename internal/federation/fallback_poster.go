package federation

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"sync"
)

// The fallback poster: the icon a video object names when the video has no
// poster of its own that this instance can size.
//
// PeerTube refuses a WHOLE video that carries no icon with an integer width
// and height (v8.3.2 videos.ts "icons", misc.ts setValidRemoteIcon), and vidra
// has two kinds of video with none to give: one with no poster at all (an
// audio-only upload has no frame to grab), and one whose poster it cannot read
// the size of. The interop lab produced both, and PeerTube stored neither
// (meta docs/evidence/peertube-interop-2026-11-17-gaps.md). So the object
// names this image too, after the poster it has, if any. Its size is stated
// because it is known — the image is made here — which keeps the rule that a
// size is never invented.

// FallbackPosterPath is where the API serves the image. It sits under /api
// because that is the one prefix a deployment's proxy already sends to the api
// (deploy/Caddyfile in the meta repo); any other path would 404 at the edge.
const FallbackPosterPath = "/api/v1/federation/poster.png"

const (
	fallbackPosterType   = "image/png"
	fallbackPosterWidth  = 640
	fallbackPosterHeight = 360
)

// FallbackPosterPNG is the image: one flat dark colour, 16:9, encoded once.
var FallbackPosterPNG = sync.OnceValue(func() []byte {
	bounds := image.Rect(0, 0, fallbackPosterWidth, fallbackPosterHeight)
	img := image.NewPaletted(bounds, color.Palette{color.RGBA{R: 0x1c, G: 0x1c, B: 0x1e, A: 0xff}})
	var buf bytes.Buffer
	_ = png.Encode(&buf, img) // a bytes.Buffer never fails a write
	return buf.Bytes()
})
