package atproto

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/vidra/vidra-core/internal/urlsafety"
)

const (
	// nsidCreateSession authenticates and resolves a DID.
	nsidCreateSession = "com.atproto.server.createSession"
	// nsidUploadBlob uploads a binary blob (the post thumbnail).
	nsidUploadBlob = "com.atproto.repo.uploadBlob"
	// nsidCreateRecord writes a record into a repo collection.
	nsidCreateRecord = "com.atproto.repo.createRecord"

	// feedPostCollection is the app.bsky.feed.post collection NSID.
	feedPostCollection = "app.bsky.feed.post"

	// xrpcTimeout bounds a single XRPC call.
	xrpcTimeout = 15 * time.Second
	// maxXRPCResponse bounds a PDS response body read (defence in depth; XRPC
	// responses here are tiny JSON documents).
	maxXRPCResponse = 1 << 20 // 1 MiB
)

// Session is the result of com.atproto.server.createSession: the resolved
// DID/handle and the access token used to authorize repo writes in the same run.
// AccessJWT is a short-lived bearer token — never log it.
type Session struct {
	DID       string
	Handle    string
	AccessJWT string
}

// Blob is the opaque blob reference returned by uploadBlob, embedded verbatim as
// the post embed's external.thumb.
type Blob = json.RawMessage

// PDSClient performs the ATProto XRPC calls the extension needs against a PDS.
// The concrete client is SSRF-guarded (https + public host, dial-time IP guard).
// Tests substitute a fake for the worker and use the real client (allowing
// loopback) against an httptest server for the link path.
type PDSClient interface {
	// CreateSession authenticates identifier + appPassword against pdsURL.
	CreateSession(ctx context.Context, pdsURL, identifier, appPassword string) (Session, error)
	// UploadBlob uploads raw bytes and returns the blob reference JSON to embed.
	UploadBlob(ctx context.Context, pdsURL string, sess Session, mime string, data []byte) (Blob, error)
	// CreateRecord writes a record into collection and returns the resulting AT-URI.
	CreateRecord(ctx context.Context, pdsURL string, sess Session, collection string, record map[string]any) (string, error)
}

// XRPCError is a non-2xx XRPC response. Its message is SAFE to log/return: it
// carries only the operation NSID, the HTTP status, and the XRPC error NAME (a
// short machine token such as "AuthenticationRequired" or "RateLimitExceeded") —
// never the PDS URL, request body, credentials, or the human error message
// (which could echo the submitted handle).
type XRPCError struct {
	Status  int
	Op      string
	ErrName string
}

func (e *XRPCError) Error() string {
	if e.ErrName != "" {
		return fmt.Sprintf("atproto: XRPC %s failed: status %d (%s)", e.Op, e.Status, e.ErrName)
	}
	return fmt.Sprintf("atproto: XRPC %s failed: status %d", e.Op, e.Status)
}

// IsRateLimited reports whether err is a 429 XRPC error (the worker backs off).
func IsRateLimited(err error) bool {
	var xe *XRPCError
	return errors.As(err, &xe) && xe.Status == http.StatusTooManyRequests
}

// httpClient is the SSRF-guarded PDS client.
type httpClient struct {
	allowPrivate bool
}

// ClientOption customises the HTTP PDS client.
type ClientOption func(*httpClient)

// WithClientAllowPrivate relaxes the SSRF guard (loopback/private + http).
// DEVELOPMENT/TEST-ONLY.
func WithClientAllowPrivate(allow bool) ClientOption {
	return func(c *httpClient) { c.allowPrivate = allow }
}

// NewHTTPClient builds the SSRF-guarded PDS client used in production wiring.
func NewHTTPClient(opts ...ClientOption) *httpClient {
	c := &httpClient{}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// validatePDSURL enforces the PDS-URL policy: an http/https, public, fetchable
// origin — and https specifically unless the guard is relaxed (dev/test). It
// returns ErrInvalidPDS on any violation.
func (c *httpClient) validatePDSURL(raw string) (string, error) {
	guard := urlsafety.Guard{AllowPrivate: c.allowPrivate}
	u, err := guard.ValidateURL(raw)
	if err != nil {
		return "", ErrInvalidPDS
	}
	if !c.allowPrivate && u.Scheme != "https" {
		return "", ErrInvalidPDS
	}
	return u.String(), nil
}

// do performs one XRPC POST and returns the (bounded) response body. accessJWT,
// when non-empty, is sent as a bearer token. contentType defaults to JSON.
func (c *httpClient) do(ctx context.Context, pdsURL, nsid, accessJWT, contentType string, body []byte) ([]byte, error) {
	base, err := c.validatePDSURL(pdsURL)
	if err != nil {
		return nil, err
	}
	guard := urlsafety.Guard{AllowPrivate: c.allowPrivate}
	client := guard.NewClient(xrpcTimeout)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/xrpc/"+nsid, bytes.NewReader(body))
	if err != nil {
		return nil, &XRPCError{Op: nsid}
	}
	if contentType == "" {
		contentType = "application/json"
	}
	req.Header.Set("Content-Type", contentType)
	if accessJWT != "" {
		req.Header.Set("Authorization", "Bearer "+accessJWT)
	}
	resp, err := client.Do(req)
	if err != nil {
		// A transport error carries no safe structured info; surface a generic
		// XRPCError with a 0 status so the worker treats it as transient.
		return nil, &XRPCError{Op: nsid}
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxXRPCResponse))
	if resp.StatusCode >= 300 {
		return nil, &XRPCError{Status: resp.StatusCode, Op: nsid, ErrName: parseXRPCErrName(respBody)}
	}
	return respBody, nil
}

// CreateSession implements PDSClient.
func (c *httpClient) CreateSession(ctx context.Context, pdsURL, identifier, appPassword string) (Session, error) {
	body, err := json.Marshal(map[string]string{"identifier": identifier, "password": appPassword})
	if err != nil {
		return Session{}, err
	}
	respBody, err := c.do(ctx, pdsURL, nsidCreateSession, "", "application/json", body)
	if err != nil {
		return Session{}, err
	}
	var out struct {
		DID       string `json:"did"`
		Handle    string `json:"handle"`
		AccessJWT string `json:"accessJwt"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return Session{}, &XRPCError{Op: nsidCreateSession}
	}
	return Session{DID: out.DID, Handle: out.Handle, AccessJWT: out.AccessJWT}, nil
}

// UploadBlob implements PDSClient.
func (c *httpClient) UploadBlob(ctx context.Context, pdsURL string, sess Session, mime string, data []byte) (Blob, error) {
	respBody, err := c.do(ctx, pdsURL, nsidUploadBlob, sess.AccessJWT, mime, data)
	if err != nil {
		return nil, err
	}
	var out struct {
		Blob json.RawMessage `json:"blob"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil || len(out.Blob) == 0 {
		return nil, &XRPCError{Op: nsidUploadBlob}
	}
	return out.Blob, nil
}

// CreateRecord implements PDSClient.
func (c *httpClient) CreateRecord(ctx context.Context, pdsURL string, sess Session, collection string, record map[string]any) (string, error) {
	body, err := json.Marshal(map[string]any{
		"repo":       sess.DID,
		"collection": collection,
		"record":     record,
	})
	if err != nil {
		return "", err
	}
	respBody, err := c.do(ctx, pdsURL, nsidCreateRecord, sess.AccessJWT, "application/json", body)
	if err != nil {
		return "", err
	}
	var out struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil || out.URI == "" {
		return "", &XRPCError{Op: nsidCreateRecord}
	}
	return out.URI, nil
}

// parseXRPCErrName extracts the machine-readable "error" name from an XRPC error
// body, if present. It deliberately ignores the human "message" (which may echo
// submitted input like the handle). Returns "" when absent/unparseable.
func parseXRPCErrName(body []byte) string {
	var out struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return ""
	}
	return out.Error
}
