package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/federation"
)

// Dereferenceable ActivityPub object ids (A29-F2, A29-F9).
//
// The AP object ids vidra mints — /videos/{uuid} for a video, /comments/{uuid}
// for a Note — sit on paths the FRONTEND owns, so A29 measured an
// `Accept: application/activity+json` getting 200 and a page of HTML. These two
// root routes are the core half of the fix; the operator's reverse proxy is the
// other half, and the shipped Caddy config now content-negotiates /videos/* and
// /comments/* to the api (meta repo).
//
// A non-AP Accept is 406, exactly as the actor and collection routes already
// answer, and for the same reason: core has no HTML, and an answer that depended
// on which process happened to receive the request would be worse than a clear
// refusal. With the proxy rule in place a browser never reaches here at all.
//
// apObjectCacheControl is PUBLIC: these documents describe public objects only,
// carry no credential, and are what a peer polls when it re-checks a video. Five
// minutes is short enough that a title edit or a deletion propagates to a
// crawler quickly and long enough that a busy peer is not re-fetching per view.
const apObjectCacheControl = "public, max-age=300"

// serveAPObject renders one ActivityPub document with the negotiation, cache and
// validator rules every object id shares.
func (s *Server) serveAPObject(c echo.Context, load func(uuid.UUID) (map[string]any, error), tombstone func(uuid.UUID) (map[string]any, error)) error {
	if !wantsActivityJSON(c.Request().Header.Get("Accept")) {
		return echo.NewHTTPError(http.StatusNotAcceptable, "objects are served as application/activity+json")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound)
	}
	doc, err := load(id)
	switch {
	case errors.Is(err, federation.ErrGone):
		// 410 + Tombstone. A peer that dereferences the Delete it was just sent
		// must be able to tell "really gone" from "this server is confused",
		// which a 200-with-a-page could never say.
		if tombstone == nil {
			return echo.NewHTTPError(http.StatusGone)
		}
		doc, err = tombstone(id)
		if err != nil {
			if errors.Is(err, federation.ErrNotFound) {
				return echo.NewHTTPError(http.StatusNotFound)
			}
			return err
		}
		return s.writeAPObject(c, http.StatusGone, doc, false)
	case errors.Is(err, federation.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound)
	case err != nil:
		return err
	}
	return s.writeAPObject(c, http.StatusOK, doc, true)
}

// writeAPObject serialises the document, stamps the AP content type, a strong
// ETag over the exact bytes, and the cache policy — then answers 304 when the
// peer already holds those bytes.
//
// The ETag is computed over the SERIALISED document rather than over a row's
// updated_at because the document is what the peer caches: a change to how we
// RENDER a video (a new field, a corrected url) must invalidate it just as a
// change to the video does.
//
// A Tombstone is deliberately NOT cached (cacheable=false): the 410 is the one
// answer that can turn back into a 200 if an operator restores from a backup,
// and a cached-for-five-minutes deletion is a worse failure than an extra fetch.
// It carries NO ETag either — the rehearsal caught one on the 410 and called it
// meaningless, which is the polite reading. A validator invites a peer to send
// If-None-Match, and this is precisely the answer that must never be revalidated
// into a 304: a 304 says "what you already have is current", and what the peer
// already has is the video.
func (s *Server) writeAPObject(c echo.Context, status int, doc map[string]any, cacheable bool) error {
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	header := c.Response().Header()
	header.Set(echo.HeaderContentType, activityJSONContentType)
	if cacheable {
		sum := sha256.Sum256(body)
		etag := `"` + hex.EncodeToString(sum[:16]) + `"`
		header.Set("ETag", etag)
		header.Set("Cache-Control", apObjectCacheControl)
		if match := c.Request().Header.Get("If-None-Match"); match == etag {
			return c.NoContent(http.StatusNotModified)
		}
	} else {
		header.Set("Cache-Control", "no-store")
	}
	return c.Blob(status, activityJSONContentType, body)
}

// handleVideoObject serves GET /videos/{uuid} as ActivityPub — the object id
// every Create, Update, Delete and inReplyTo we emit points at.
func (s *Server) handleVideoObject(c echo.Context) error {
	ctx := c.Request().Context()
	return s.serveAPObject(c,
		func(id uuid.UUID) (map[string]any, error) { return s.fedsvc.VideoObject(ctx, id) },
		func(id uuid.UUID) (map[string]any, error) { return s.fedsvc.VideoTombstone(ctx, id) },
	)
}

// handleNoteObject serves GET /comments/{uuid} as ActivityPub — the Note id a
// remote reply threads against.
func (s *Server) handleNoteObject(c echo.Context) error {
	ctx := c.Request().Context()
	return s.serveAPObject(c,
		func(id uuid.UUID) (map[string]any, error) { return s.fedsvc.NoteObject(ctx, id) },
		nil,
	)
}
