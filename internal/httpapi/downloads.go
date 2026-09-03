package httpapi

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/delivery"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

// videoDownloadFileView describes one browser-downloadable representation. HLS
// renditions point at stable, progressive MP4 remuxes (not .m3u8 manifests), so
// each selected quality arrives as one file with audio muxed into it. A second
// URL is present when the caller may choose a video-only copy.
type videoDownloadFileView struct {
	Kind         string `json:"kind"` // original | webm | hls | audio | subtitle
	URL          string `json:"url"`
	VideoOnlyURL string `json:"video_only_url,omitempty"`
	Filename     string `json:"filename"`
	ContentType  string `json:"content_type"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
	OriginalName string `json:"original_name,omitempty"`
	Height       int32  `json:"height,omitempty"`
	Width        int32  `json:"width,omitempty"`
	Language     string `json:"language,omitempty"`
	Label        string `json:"label,omitempty"`
}

type videoDownloadResponse struct {
	Files []videoDownloadFileView `json:"files"`
}

// videoForDownload is the single authorization boundary for official download
// metadata and bytes — EVERY /videos/{id}/download route resolves through it.
// Moderator/admin callers bypass the download gates and may download any local
// video for moderation. Everyone else needs the LAYERED download policy
// (config-parity W9): the instance-wide downloads_enabled setting AND the
// video's own download_enabled flag — instance gate off means no downloads
// regardless of the per-video flag — plus the ordinary detail visibility policy
// (privacy, scheduled/quarantine/block, and password gate). It also returns the
// identity FileForView should use; privileged callers impersonate the real
// owner only for that storage lookup.
func (s *Server) videoForDownload(c echo.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, uuid.UUID, bool, error) {
	viewerID, role, authed := principalFromContext(c)
	privileged := authed && isStaff(role)
	if !privileged && !s.downloadsEnabled() {
		return sqlcgen.GetVideoByIDRow{}, uuid.Nil, false, &FeatureDisabledError{Feature: "downloads"}
	}
	if privileged {
		v, err := s.videosvc.GetByID(c.Request().Context(), id)
		if err != nil {
			if errors.Is(err, video.ErrNotFound) {
				return sqlcgen.GetVideoByIDRow{}, uuid.Nil, false, echo.NewHTTPError(http.StatusNotFound, "video not found")
			}
			return sqlcgen.GetVideoByIDRow{}, uuid.Nil, false, err
		}
		return v, v.OwnerID, true, nil
	}
	v, err := s.videoVisibleForMedia(c, id)
	if err != nil {
		return sqlcgen.GetVideoByIDRow{}, uuid.Nil, false, err
	}
	// Per-video download policy (config-parity W9), checked AFTER visibility so
	// a hidden video keeps answering 404, and only once the instance gate above
	// passed (the layering contract).
	if !v.DownloadEnabled {
		return sqlcgen.GetVideoByIDRow{}, uuid.Nil, false, &FeatureDisabledError{Feature: "downloads"}
	}
	return v, viewerID, authed, nil
}

// handleGetVideoDownloads lists only actual single-file download assets. HLS
// playback manifests are intentionally absent: every HLS row is a progressive
// MP4 remux at that rung, with a companion video-only URL when available.
func (s *Server) handleGetVideoDownloads(c echo.Context) error {
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	v, fileViewerID, fileViewerAuthed, err := s.videoForDownload(c, id)
	if err != nil {
		return err
	}

	files := make([]videoDownloadFileView, 0, 8)
	base := "/api/v1/videos/" + id.String() + "/download"

	if f, ferr := s.videosvc.FileForView(ctx, id, fileViewerID, fileViewerAuthed, "original"); ferr == nil {
		filename := safeDownloadFilename(f.OriginalName, "video-"+id.String()+filepath.Ext(f.OriginalName))
		entry := videoDownloadFileView{
			Kind:         "original",
			URL:          base + "/original",
			Filename:     filename,
			ContentType:  f.ContentType,
			SizeBytes:    f.SizeBytes,
			OriginalName: f.OriginalName,
		}
		if md, ok, merr := s.videosvc.GetMetadata(ctx, id); merr == nil && ok {
			if md.Height != nil {
				entry.Height = *md.Height
			}
			if md.Width != nil {
				entry.Width = *md.Width
			}
		}
		files = append(files, entry)
	}

	if f, ferr := s.videosvc.FileForView(ctx, id, fileViewerID, fileViewerAuthed, "webm"); ferr == nil {
		files = append(files, videoDownloadFileView{
			Kind:         "webm",
			URL:          base + "/webm",
			Filename:     "video-" + id.String() + "-vp9.webm",
			ContentType:  f.ContentType,
			SizeBytes:    f.SizeBytes,
			OriginalName: f.OriginalName,
		})
	}

	if s.transcodesvc != nil && s.media != nil {
		if sp, ok := s.transcodesvc.Playlist(ctx, id); ok && sp.State == "ready" && sp.MasterKey != "" {
			for _, rendition := range s.transcodesvc.Renditions(ctx, id) {
				muxedKey := media.HLSDownloadKey(rendition.KeyPrefix, true)
				muxedSize, exists, sizeErr := s.downloadObjectSize(ctx, muxedKey)
				if sizeErr != nil {
					return sizeErr
				}
				if !exists {
					continue
				}
				videoOnlyURL := ""
				if _, videoOnlyExists, verr := s.downloadObjectSize(ctx, media.HLSDownloadKey(rendition.KeyPrefix, false)); verr != nil {
					return verr
				} else if videoOnlyExists {
					videoOnlyURL = base + "/hls/" + strconv.Itoa(int(rendition.Height)) + "?audio=false"
				}
				files = append(files, videoDownloadFileView{
					Kind:         "hls",
					URL:          base + "/hls/" + strconv.Itoa(int(rendition.Height)),
					VideoOnlyURL: videoOnlyURL,
					Filename:     fmt.Sprintf("video-%s-%dp.mp4", id, rendition.Height),
					ContentType:  media.HLSMP4ContentType,
					SizeBytes:    muxedSize,
					Height:       rendition.Height,
					Width:        rendition.Width,
				})
			}
			if audioSize, exists, aerr := s.downloadObjectSize(ctx, media.HLSAudioDownloadKey(sp.MasterKey)); aerr != nil {
				return aerr
			} else if exists {
				files = append(files, videoDownloadFileView{
					Kind:        "audio",
					URL:         base + "/audio",
					Filename:    "video-" + id.String() + "-audio.m4a",
					ContentType: media.HLSM4AContentType,
					SizeBytes:   audioSize,
				})
			}
		}
	}

	if captions, cerr := s.videosvc.ListCaptions(ctx, id); cerr != nil {
		return cerr
	} else {
		for _, caption := range captions {
			files = append(files, videoDownloadFileView{
				Kind:        "subtitle",
				URL:         base + "/subtitles/" + caption.Language,
				Filename:    "video-" + id.String() + "-" + caption.Language + ".vtt",
				ContentType: "text/vtt; charset=utf-8",
				Language:    caption.Language,
				Label:       caption.Label,
			})
		}
	}

	_ = v // authorization intentionally resolves the row even when no files exist
	return c.JSON(http.StatusOK, videoDownloadResponse{Files: files})
}

func (s *Server) handleDownloadVideoOriginal(c echo.Context) error {
	return s.downloadStoredVideoFile(c, "original", "video-original")
}

func (s *Server) handleDownloadVideoWebM(c echo.Context) error {
	return s.downloadStoredVideoFile(c, "webm", "video-vp9.webm")
}

func (s *Server) downloadStoredVideoFile(c echo.Context, kind, fallbackName string) error {
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	v, viewerID, authed, err := s.videoForDownload(c, id)
	if err != nil {
		return err
	}
	f, err := s.videosvc.FileForView(c.Request().Context(), id, viewerID, authed, kind)
	if err != nil {
		return videoError(err)
	}
	filename := fallbackName
	if kind == "original" {
		filename = safeDownloadFilename(f.OriginalName, "video-"+id.String()+filepath.Ext(f.OriginalName))
	}
	return s.serveMediaAsset(c, mediaAsset{
		key:         f.StorageKey,
		contentType: f.ContentType,
		class:       delivery.ClassDownload,
		filename:    filename,
		eligible:    s.publicDownload(v),
		notFound:    "video not found",
	})
}

// publicDownload reports whether this download is the one an anonymous visitor
// could take: a public, published video with BOTH download gates open.
//
// It re-states videoForDownload's non-privileged path deliberately. A
// moderator's download of the same object is authorised — they bypass the
// gates — but it is authorised for THEM, and a signed URL is a transferable
// credential. Downloads being switched off instance-wide has to mean no
// download URL leaves the building, not "no download URL except the one the
// moderator's browser can be talked into fetching".
func (s *Server) publicDownload(v sqlcgen.GetVideoByIDRow) bool {
	return s.publicDownloadOf(v.Privacy, v.State, v.DownloadEnabled)
}

// publicDownloadOf is publicDownload over plain values, for the callers that
// hold a different row shape of the same video (the update handler writes and
// re-reads a sqlcgen.Video). Same three-way AND, one definition — the fence and
// the purge trigger that enforces it must never be able to disagree.
func (s *Server) publicDownloadOf(privacy, state string, downloadEnabled bool) bool {
	return publicVideoForIPFS(privacy, state) && s.downloadsEnabled() && downloadEnabled
}

func (s *Server) handleDownloadHLSRendition(c echo.Context) error {
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	height, err := strconv.Atoi(c.Param("height"))
	if err != nil || height <= 0 {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	v, _, _, err := s.videoForDownload(c, id)
	if err != nil {
		return err
	}
	includeAudio := !strings.EqualFold(c.QueryParam("audio"), "false")
	rendition, key, err := s.resolveHLSDownload(c.Request().Context(), id, height, includeAudio)
	if err != nil {
		return err
	}
	filename := fmt.Sprintf("video-%s-%dp.mp4", id, rendition.Height)
	if !includeAudio {
		filename = fmt.Sprintf("video-%s-%dp-video-only.mp4", id, rendition.Height)
	}
	return s.serveMediaAsset(c, mediaAsset{
		key:         key,
		contentType: media.HLSMP4ContentType,
		class:       delivery.ClassDownload,
		filename:    filename,
		eligible:    s.publicDownload(v),
		notFound:    "video not found",
	})
}

func (s *Server) handleDownloadHLSAudio(c echo.Context) error {
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	v, _, _, err := s.videoForDownload(c, id)
	if err != nil {
		return err
	}
	if s.transcodesvc == nil {
		return echo.NewHTTPError(http.StatusNotFound, "download not found")
	}
	sp, ok := s.transcodesvc.Playlist(c.Request().Context(), id)
	if !ok || sp.State != "ready" || sp.MasterKey == "" {
		return echo.NewHTTPError(http.StatusNotFound, "download not found")
	}
	key := media.HLSAudioDownloadKey(sp.MasterKey)
	if _, exists, err := s.downloadObjectSize(c.Request().Context(), key); err != nil {
		return err
	} else if !exists {
		return echo.NewHTTPError(http.StatusNotFound, "download not found")
	}
	return s.serveMediaAsset(c, mediaAsset{
		key:         key,
		contentType: media.HLSM4AContentType,
		class:       delivery.ClassAudio,
		filename:    "video-" + id.String() + "-audio.m4a",
		eligible:    s.publicDownload(v),
		notFound:    "video not found",
	})
}

// resolveHLSDownload accepts only assets from a ready playlist and proves the
// selected object exists before the handler attaches download headers. Failed
// retranscodes may leave rendition rows behind; those must not expose stale or
// partial files through a guessed URL.
func (s *Server) resolveHLSDownload(ctx context.Context, id uuid.UUID, height int, includeAudio bool) (sqlcgen.VideoRendition, string, error) {
	if s.transcodesvc == nil {
		return sqlcgen.VideoRendition{}, "", echo.NewHTTPError(http.StatusNotFound, "download not found")
	}
	sp, ok := s.transcodesvc.Playlist(ctx, id)
	if !ok || sp.State != "ready" || sp.MasterKey == "" {
		return sqlcgen.VideoRendition{}, "", echo.NewHTTPError(http.StatusNotFound, "download not found")
	}
	for _, rendition := range s.transcodesvc.Renditions(ctx, id) {
		if int(rendition.Height) != height {
			continue
		}
		key := media.HLSDownloadKey(rendition.KeyPrefix, includeAudio)
		if _, exists, err := s.downloadObjectSize(ctx, key); err != nil {
			return sqlcgen.VideoRendition{}, "", err
		} else if !exists {
			return sqlcgen.VideoRendition{}, "", echo.NewHTTPError(http.StatusNotFound, "download not found")
		}
		return rendition, key, nil
	}
	return sqlcgen.VideoRendition{}, "", echo.NewHTTPError(http.StatusNotFound, "download not found")
}

func (s *Server) handleDownloadVideoSubtitle(c echo.Context) error {
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	if _, _, _, err := s.videoForDownload(c, id); err != nil {
		return err
	}
	language := strings.TrimSpace(c.Param("lang"))
	rc, err := s.videosvc.OpenCaption(c.Request().Context(), id, language)
	if err != nil {
		if errors.Is(err, video.ErrCaptionNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "caption not found")
		}
		return err
	}
	defer func() { _ = rc.Close() }()
	setAttachmentHeader(c, "video-"+id.String()+"-"+language+".vtt")
	// Caption bytes come back as a stream from video.Service, which never
	// surfaces the storage key, so this route keeps the API-proxy path and takes
	// the cache policy alone (see docs/operations.md — captions are deliberately
	// outside presigned delivery).
	setMediaCacheControl(c, delivery.ClassCaption)
	return c.Stream(http.StatusOK, "text/vtt; charset=utf-8", rc)
}

// handleStreamVideoWebM serves the optional progressive VP9 alternate for
// playback. It follows the ordinary media visibility policy (unlike official
// download routes, the downloads_enabled toggle and moderator bypass do not
// apply to playback).
func (s *Server) handleStreamVideoWebM(c echo.Context) error {
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	v, err := s.videoVisibleForMedia(c, id)
	if err != nil {
		return err
	}
	viewerID, _, authed := principalFromContext(c)
	f, err := s.videosvc.FileForView(c.Request().Context(), id, viewerID, authed, "webm")
	if err != nil {
		return videoError(err)
	}
	return s.serveMediaAsset(c, mediaAsset{
		key:         f.StorageKey,
		contentType: f.ContentType,
		class:       delivery.ClassWebM,
		eligible:    publicVideoForIPFS(v.Privacy, v.State),
		notFound:    "video not found",
	})
}

// downloadObjectSize returns (size, exists) without opening every remote object.
// Local storage exposes a path for an exact stat; remote backends use their
// cheap existence/HEAD operation and omit size rather than issuing a GET per
// rendition.
func (s *Server) downloadObjectSize(ctx context.Context, key string) (int64, bool, error) {
	if s.media == nil {
		return 0, false, nil
	}
	if paths, ok := s.media.(storage.PathProvider); ok {
		path, err := paths.Path(key)
		if err != nil {
			return 0, false, err
		}
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return 0, false, nil
			}
			return 0, false, err
		}
		if !info.Mode().IsRegular() {
			return 0, false, nil
		}
		return info.Size(), true, nil
	}
	exists, err := s.media.Exists(ctx, key)
	return 0, exists, err
}

func safeDownloadFilename(name, fallback string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return fallback
	}
	return name
}

// attachmentHeader formats the Content-Disposition value for a download. It is
// split out from setAttachmentHeader because a presigned redirect has to send
// the SAME value to the object store as a response-header override — the
// browser never sees this route's headers once it follows the redirect, so a
// second formatting of the filename would be a second chance to disagree.
func attachmentHeader(filename string) string {
	header := mime.FormatMediaType("attachment", map[string]string{"filename": filename})
	if header == "" {
		header = "attachment"
	}
	return header
}

func setAttachmentHeader(c echo.Context, filename string) {
	c.Response().Header().Set(echo.HeaderContentDisposition, attachmentHeader(filename))
}
