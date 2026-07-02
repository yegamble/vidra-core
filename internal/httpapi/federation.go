package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/httpsig"
	"github.com/vidra/vidra-core/internal/version"
)

// maxInboxBytes bounds an inbound activity body.
const maxInboxBytes = 1 << 20 // 1 MiB

// NodeInfo (https://nodeinfo.diaspora.software/) is the fediverse's instance
// self-description: software name/version, supported protocols, whether signup
// is open, and coarse usage counts. Remote servers and crawlers read it to learn
// what this instance runs. These endpoints are mounted only when federation is
// enabled (see routes()); they are a federation contract, not the REST API, so
// they are documented in .ralph/specs/federation.md rather than api/openapi.yaml.

const nodeInfo21Rel = "http://nodeinfo.diaspora.software/ns/schema/2.1"

// nodeInfo21ContentType is the profile content type NodeInfo 2.1 documents are
// conventionally served with.
const nodeInfo21ContentType = `application/json; profile="http://nodeinfo.diaspora.software/ns/schema/2.1#"`

type nodeInfoLink struct {
	Rel  string `json:"rel"`
	Href string `json:"href"`
}

type nodeInfoDiscovery struct {
	Links []nodeInfoLink `json:"links"`
}

// handleNodeInfoDiscovery serves /.well-known/nodeinfo — the pointer to the
// versioned document.
func (s *Server) handleNodeInfoDiscovery(c echo.Context) error {
	return c.JSON(http.StatusOK, nodeInfoDiscovery{Links: []nodeInfoLink{{
		Rel:  nodeInfo21Rel,
		Href: s.cfg.PublicBaseURL + "/nodeinfo/2.1",
	}}})
}

type nodeInfoSoftware struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type nodeInfoServices struct {
	Inbound  []string `json:"inbound"`
	Outbound []string `json:"outbound"`
}

type nodeInfoUsers struct {
	Total int64 `json:"total"`
}

type nodeInfoUsage struct {
	Users         nodeInfoUsers `json:"users"`
	LocalPosts    int64         `json:"localPosts"`
	LocalComments int64         `json:"localComments"`
}

type nodeInfo21Document struct {
	Version           string           `json:"version"`
	Software          nodeInfoSoftware `json:"software"`
	Protocols         []string         `json:"protocols"`
	Services          nodeInfoServices `json:"services"`
	OpenRegistrations bool             `json:"openRegistrations"`
	Usage             nodeInfoUsage    `json:"usage"`
	Metadata          map[string]any   `json:"metadata"`
}

// handleNodeInfo21 serves /nodeinfo/2.1 — the NodeInfo 2.1 document with live
// usage counts.
func (s *Server) handleNodeInfo21(c echo.Context) error {
	usage, err := s.fedsvc.NodeInfoUsage(c.Request().Context())
	if err != nil {
		return err
	}
	doc := nodeInfo21Document{
		Version:           "2.1",
		Software:          nodeInfoSoftware{Name: "vidra", Version: version.Version},
		Protocols:         []string{"activitypub"},
		Services:          nodeInfoServices{Inbound: []string{}, Outbound: []string{}},
		OpenRegistrations: s.cfg.RegistrationEnabled,
		Usage: nodeInfoUsage{
			Users:         nodeInfoUsers{Total: usage.Users},
			LocalPosts:    usage.LocalPosts,
			LocalComments: usage.LocalComments,
		},
		Metadata: map[string]any{"nodeName": s.cfg.InstanceName},
	}
	// Set the profile content type before c.JSON (Echo only sets a default when
	// none is present), so consumers see the NodeInfo 2.1 profile.
	c.Response().Header().Set(echo.HeaderContentType, nodeInfo21ContentType)
	return c.JSON(http.StatusOK, doc)
}

// activityJSONContentType is what actor documents are served as.
const activityJSONContentType = "application/activity+json; charset=utf-8"

// wantsActivityJSON reports whether the Accept header asks for ActivityPub JSON.
// Actor endpoints serve only JSON-LD; the human-facing profile is vidra-user's.
func wantsActivityJSON(accept string) bool {
	a := strings.ToLower(accept)
	return strings.Contains(a, "application/activity+json") || strings.Contains(a, "application/ld+json")
}

func (s *Server) handleAccountActor(c echo.Context) error {
	return s.serveActor(c, s.fedsvc.AccountActor)
}

func (s *Server) handleChannelActor(c echo.Context) error {
	return s.serveActor(c, s.fedsvc.ChannelActor)
}

// serveActor content-negotiates and serves an actor document. A non-AP Accept
// gets 406; an unknown/inactive actor gets 404.
func (s *Server) serveActor(c echo.Context, fetch func(context.Context, string) (*federation.Actor, error)) error {
	if !wantsActivityJSON(c.Request().Header.Get("Accept")) {
		return echo.NewHTTPError(http.StatusNotAcceptable, "actor documents are served as application/activity+json")
	}
	actor, err := fetch(c.Request().Context(), c.Param("handle"))
	if errors.Is(err, federation.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound)
	}
	if err != nil {
		return err
	}
	c.Response().Header().Set(echo.HeaderContentType, activityJSONContentType)
	return c.JSON(http.StatusOK, actor)
}

// handleWebFinger serves /.well-known/webfinger — resolving acct:name@domain to
// its actor URL.
func (s *Server) handleWebFinger(c echo.Context) error {
	resource := c.QueryParam("resource")
	if resource == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "resource query parameter is required")
	}
	jrd, err := s.fedsvc.WebFinger(c.Request().Context(), resource)
	if errors.Is(err, federation.ErrBadResource) {
		return echo.NewHTTPError(http.StatusBadRequest, "malformed resource")
	}
	if errors.Is(err, federation.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound)
	}
	if err != nil {
		return err
	}
	c.Response().Header().Set(echo.HeaderContentType, "application/jrd+json; charset=utf-8")
	return c.JSON(http.StatusOK, jrd)
}

// handleInbox verifies an inbound activity's HTTP signature (resolving the
// signer's key via the remote-actor cache), then dispatches it. Used for the
// shared /inbox and per-actor inbox routes; the target is read from the activity,
// not the path. Bad signature → 401; malformed/mismatched activity → 400.
func (s *Server) handleInbox(c echo.Context) error {
	body, err := io.ReadAll(io.LimitReader(c.Request().Body, maxInboxBytes))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "could not read request body")
	}
	verifier := httpsig.Verifier{ResolveKey: s.fedsvc.ResolveKey}
	keyID, err := verifier.Verify(c.Request().Context(), c.Request(), body)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "signature verification failed")
	}
	signerActorURL, _, _ := strings.Cut(keyID, "#")
	if err := s.fedsvc.HandleInbox(c.Request().Context(), signerActorURL, body); err != nil {
		if errors.Is(err, federation.ErrBadResource) || errors.Is(err, federation.ErrActorMismatch) {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid activity")
		}
		return echo.NewHTTPError(http.StatusUnprocessableEntity, "could not process activity")
	}
	return c.NoContent(http.StatusAccepted)
}
