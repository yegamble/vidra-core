package httpapi

import (
    "net/http"

    "athena/internal/usecase"
)

// EncodingStatusHandler reports counts of jobs by status
func EncodingStatusHandler(repo usecase.EncodingRepository) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        counts, err := repo.GetJobCounts(r.Context())
        if err != nil {
            WriteError(w, http.StatusInternalServerError, err)
            return
        }
        WriteJSON(w, http.StatusOK, map[string]interface{}{
            "pending":    counts["pending"],
            "processing": counts["processing"],
            "completed":  counts["completed"],
            "failed":     counts["failed"],
        })
    }
}
