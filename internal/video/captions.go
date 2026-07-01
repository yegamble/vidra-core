package video

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// maxCaptionSize bounds a caption upload (WebVTT is text; a few MB is generous).
const maxCaptionSize = 4 << 20 // 4 MiB

// langPattern is a permissive BCP-47-ish language tag: a 2–3 letter primary
// subtag with optional hyphen-separated subtags (e.g. "en", "pt-BR", "zh-Hans").
var langPattern = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{1,8})*$`)

// normalizeLang trims the language tag; it does not attempt full BCP-47
// canonicalisation (the tag is validated against langPattern and stored as given).
func normalizeLang(lang string) string { return strings.TrimSpace(lang) }

// captionKey is the storage object key for a video's caption in a language.
func captionKey(videoID uuid.UUID, language string) string {
	return fmt.Sprintf("captions/%s/%s.vtt", videoID, language)
}

// CaptionInput carries a caption upload.
type CaptionInput struct {
	Language string
	Label    string
	Reader   io.Reader
}

// AddCaption stores (or replaces) a WebVTT caption track for a video owned by
// ownerID. The language must be a valid tag and the file must be WebVTT. A
// non-owner or unknown video → ErrForbidden / ErrNotFound; a bad language or
// non-WebVTT body → ErrInvalidCaption.
func (s *Service) AddCaption(ctx context.Context, ownerID, videoID uuid.UUID, in CaptionInput) (sqlcgen.Caption, error) {
	if s.blobs == nil {
		return sqlcgen.Caption{}, ErrStorageUnavailable
	}
	lang := normalizeLang(in.Language)
	if !langPattern.MatchString(lang) {
		return sqlcgen.Caption{}, ErrInvalidCaption
	}
	v, err := s.GetByID(ctx, videoID)
	if err != nil {
		return sqlcgen.Caption{}, err
	}
	if v.OwnerID != ownerID {
		return sqlcgen.Caption{}, ErrForbidden
	}

	// Read the file (bounded) and require the WebVTT signature.
	data, err := io.ReadAll(io.LimitReader(in.Reader, maxCaptionSize+1))
	if err != nil {
		return sqlcgen.Caption{}, err
	}
	if len(data) > maxCaptionSize {
		return sqlcgen.Caption{}, ErrInvalidCaption
	}
	if !isWebVTT(data) {
		return sqlcgen.Caption{}, ErrInvalidCaption
	}

	key := captionKey(videoID, lang)
	if _, err := s.blobs.Put(ctx, key, bytes.NewReader(data)); err != nil {
		return sqlcgen.Caption{}, err
	}
	return s.repo.UpsertCaption(ctx, sqlcgen.UpsertCaptionParams{
		VideoID:    videoID,
		Language:   lang,
		Label:      strings.TrimSpace(in.Label),
		StorageKey: key,
	})
}

// ListCaptions returns a video's caption tracks (metadata), ordered by language.
func (s *Service) ListCaptions(ctx context.Context, videoID uuid.UUID) ([]sqlcgen.Caption, error) {
	return s.repo.ListCaptionsByVideo(ctx, videoID)
}

// OpenCaption returns the stored WebVTT bytes for a video's caption in a
// language. An unknown language → ErrCaptionNotFound.
func (s *Service) OpenCaption(ctx context.Context, videoID uuid.UUID, language string) (io.ReadCloser, error) {
	if s.blobs == nil {
		return nil, ErrStorageUnavailable
	}
	capt, err := s.repo.GetCaptionByLang(ctx, sqlcgen.GetCaptionByLangParams{
		VideoID:  videoID,
		Language: normalizeLang(language),
	})
	if err != nil {
		return nil, ErrCaptionNotFound
	}
	return s.blobs.Open(ctx, capt.StorageKey)
}

// DeleteCaption removes a video's caption track for a language (idempotent). A
// non-owner or unknown video → ErrForbidden / ErrNotFound.
func (s *Service) DeleteCaption(ctx context.Context, ownerID, videoID uuid.UUID, language string) error {
	v, err := s.GetByID(ctx, videoID)
	if err != nil {
		return err
	}
	if v.OwnerID != ownerID {
		return ErrForbidden
	}
	lang := normalizeLang(language)
	// Best-effort blob cleanup: look up the key, delete the row, then the object.
	if capt, gerr := s.repo.GetCaptionByLang(ctx, sqlcgen.GetCaptionByLangParams{VideoID: videoID, Language: lang}); gerr == nil && s.blobs != nil {
		_ = s.blobs.Delete(ctx, capt.StorageKey)
	}
	_, err = s.repo.DeleteCaption(ctx, sqlcgen.DeleteCaptionParams{VideoID: videoID, Language: lang})
	return err
}

// isWebVTT reports whether data begins with the WebVTT signature (optionally
// after a UTF-8 BOM), per the WebVTT spec: the file must start with "WEBVTT"
// followed by end-of-file, a newline, a space, or a tab.
func isWebVTT(data []byte) bool {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}) // strip UTF-8 BOM
	if !bytes.HasPrefix(data, []byte("WEBVTT")) {
		return false
	}
	rest := data[len("WEBVTT"):]
	if len(rest) == 0 {
		return true
	}
	switch rest[0] {
	case '\n', '\r', ' ', '\t':
		return true
	}
	return false
}
