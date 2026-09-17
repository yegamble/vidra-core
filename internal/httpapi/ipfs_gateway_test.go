package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vidra/vidra-core/internal/ipfs"
)

type gatewayMirror struct {
	fakeIPFSMirror
	allow bool
	err   error
	calls int
}

func (m *gatewayMirror) PublicGatewayRootAllowed(context.Context, string) (bool, error) {
	m.calls++
	return m.allow, m.err
}

func TestIPFSGatewayAuthorization(t *testing.T) {
	root := "/ipfs/" + ipfs.DirCIDv1([]byte("gateway"))
	for _, tc := range []struct {
		name, method, uri string
		allow             bool
		err               error
		want              int
	}{
		{"public root", "GET", root, true, nil, 204},
		{"HLS descendant", "HEAD", root + "/720p/seg_00000.ts", true, nil, 204},
		{"withdrawn", "GET", root, false, nil, 404},
		{"unavailable ledger", "GET", root, true, errors.New("unavailable"), 404},
		{"write", "POST", root, true, nil, 404},
		{"missing method", "", root, true, nil, 404},
		{"missing uri", "GET", "", true, nil, 404},
		{"unknown cid", "GET", "/ipfs/not-a-cid", true, nil, 404},
		{"traversal", "GET", root + "/../other", true, nil, 404},
		{"escaped traversal", "GET", root + "/%2e%2e/other", true, nil, 404},
		{"double encoding", "GET", root + "/%252e%252e/other", true, nil, 404},
		{"backslash", "GET", root + "/foo\\bar", true, nil, 404},
		{"duplicate slash", "GET", root + "//file", true, nil, 404},
		{"query override", "GET", root + "?format=car", true, nil, 404},
		{"absolute uri", "GET", "https://other.test" + root, true, nil, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &gatewayMirror{allow: tc.allow, err: tc.err}
			s := ipfsServer(t, testConfig(), WithIPFSMirrorService(m))
			req := httptest.NewRequest(http.MethodGet, "/api/v1/ipfs/gateway/authorize", nil)
			req.Header.Set("X-Forwarded-Method", tc.method)
			req.Header.Set("X-Forwarded-Uri", tc.uri)
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.want, rec.Body.String())
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("authorization must never be cached")
			}
		})
	}
}
