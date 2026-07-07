package httpapi

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"testing"
)

// encodePNG returns a real PNG of the given dimensions (solid color).
func encodePNG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: 200, G: 50, B: 50, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	return buf.String()
}

func encodeJPEG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("jpeg encode: %v", err)
	}
	return buf.String()
}

func encodeGIF(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatalf("gif encode: %v", err)
	}
	return buf.String()
}

// TestDMAttachmentDimensions proves the D1 probe: images echo intrinsic
// width/height on upload and in the message list; non-image and corrupt uploads
// succeed with the dimensions omitted; an absurdly large image is rejected 422.
func TestDMAttachmentDimensions(t *testing.T) {
	srv := videoServer(t)
	adaTok, _, bobTok, _, convID := startDMConversation(t, srv)

	webp, err := os.ReadFile("testdata/tiny.webp") // real 7x5 lossless webp fixture
	if err != nil {
		t.Fatalf("read webp fixture: %v", err)
	}

	cases := []struct {
		name        string
		filename    string
		contentType string
		content     string
		wantW       int32
		wantH       int32
		wantDims    bool
	}{
		{"png", "p.png", "image/png", encodePNG(t, 4, 6), 4, 6, true},
		{"jpeg", "p.jpg", "image/jpeg", encodeJPEG(t, 8, 3), 8, 3, true},
		{"gif", "p.gif", "image/gif", encodeGIF(t, 5, 9), 5, 9, true},
		{"webp", "p.webp", "image/webp", string(webp), 7, 5, true},
		{"corrupt-image", "c.png", "image/png", "\x89PNGnot-a-real-image", 0, 0, false},
		{"non-image", "d.pdf", "application/pdf", "%PDF-1.4 tiny", 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := uploadAttachment(srv, convID, tc.filename, tc.contentType, tc.content, adaTok)
			if rec.Code != http.StatusCreated {
				t.Fatalf("upload = %d, want 201; body=%s", rec.Code, rec.Body.String())
			}
			var up uploadAttachmentResponse
			_ = json.Unmarshal(rec.Body.Bytes(), &up)
			if tc.wantDims {
				if up.Width == nil || up.Height == nil || *up.Width != tc.wantW || *up.Height != tc.wantH {
					t.Fatalf("upload dims = (%v,%v), want (%d,%d)", up.Width, up.Height, tc.wantW, tc.wantH)
				}
			} else if up.Width != nil || up.Height != nil {
				t.Fatalf("expected no dims, got (%v,%v)", up.Width, up.Height)
			}

			// The dimensions also survive on the message-list projection.
			send := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+convID+"/messages",
				`{"body":"x","attachment_ids":["`+up.AttachmentID+`"]}`, adaTok)
			if send.Code != http.StatusCreated {
				t.Fatalf("send = %d; body=%s", send.Code, send.Body.String())
			}
			list := sendJSONAuth(srv, http.MethodGet, "/api/v1/conversations/"+convID+"/messages?limit=1", "", bobTok)
			var mlr messageListResponse
			_ = json.Unmarshal(list.Body.Bytes(), &mlr)
			if len(mlr.Messages) == 0 || len(mlr.Messages[0].Attachments) != 1 {
				t.Fatalf("list attachments = %+v", mlr.Messages)
			}
			gotA := mlr.Messages[0].Attachments[0]
			if tc.wantDims {
				if gotA.Width == nil || gotA.Height == nil || *gotA.Width != tc.wantW || *gotA.Height != tc.wantH {
					t.Fatalf("list dims = (%v,%v), want (%d,%d)", gotA.Width, gotA.Height, tc.wantW, tc.wantH)
				}
			} else if gotA.Width != nil || gotA.Height != nil {
				t.Fatalf("list expected no dims, got (%v,%v)", gotA.Width, gotA.Height)
			}
		})
	}

	// An absurdly large image (> 20000px) is rejected 422 (decompression-bomb-
	// shaped metadata), never stored.
	big := encodePNG(t, 20001, 1)
	if rec := uploadAttachment(srv, convID, "huge.png", "image/png", big, adaTok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("oversize-dimension upload = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}
