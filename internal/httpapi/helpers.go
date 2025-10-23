package httpapi

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
)

// getBoolParam extracts a boolean query parameter with a default value
func getBoolParam(r *http.Request, key string, defaultValue bool) bool {
	val := r.URL.Query().Get(key)
	if val == "" {
		return defaultValue
	}

	b, err := strconv.ParseBool(val)
	if err != nil {
		return defaultValue
	}

	return b
}

// respondError writes an error response in JSON format
func respondError(w http.ResponseWriter, statusCode int, message string, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	errMsg := message
	if err != nil {
		errMsg = message + ": " + err.Error()
	}

	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": errMsg,
	})
}

// respondJSON writes a JSON response with the given status code and data
func respondJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("Failed to encode JSON response: %v", err)
	}
}
