package video

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	chi "github.com/go-chi/chi/v5"
)

func TestStreamVideo_DefaultQuality(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/videos/123e4567-e89b-12d3-a456-426614174000/stream", nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", "123e4567-e89b-12d3-a456-426614174000")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))

	rr := httptest.NewRecorder()
	StreamVideo(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if body := rr.Body.String(); !strings.Contains(body, "720p") {
		t.Fatalf("expected default quality playlist, got %s", body)
	}
}

func TestStreamVideo_CustomQuality(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/videos/123e4567-e89b-12d3-a456-426614174000/stream?quality=1080p", nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", "123e4567-e89b-12d3-a456-426614174000")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))

	rr := httptest.NewRecorder()
	StreamVideo(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if body := rr.Body.String(); !strings.Contains(body, "1080p") {
		t.Fatalf("expected 1080p in playlist, got %s", body)
	}
}

func TestStreamVideo_InvalidQuality(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/videos/123e4567-e89b-12d3-a456-426614174000/stream?quality=999p", nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", "123e4567-e89b-12d3-a456-426614174000")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))

	rr := httptest.NewRecorder()
	StreamVideo(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}
