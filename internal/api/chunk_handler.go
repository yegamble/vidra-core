package api

import (
	"github.com/go-chi/chi/v5"

	"gotube/internal/chunk"
)

// ChunkHandler exposes endpoints for chunked uploads. It delegates to a
// ChunkedUploadManager. Additional logic (e.g. authentication or
// notifying the processing pipeline) can be added here.
type ChunkHandler struct {
	Manager *chunk.ChunkedUploadManager
}

// RegisterRoutes registers the chunk upload endpoint. Clients send
// multi‑part POST requests to /videos/upload-chunk with fields
// upload_id, chunk_number, total_chunks and file "chunk".
func (h *ChunkHandler) RegisterRoutes(r chi.Router) {
	r.Post("/upload-chunk", h.Manager.HandleChunkUpload)
}
