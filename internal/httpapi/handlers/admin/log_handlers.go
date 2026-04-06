package admin

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
)

// maxLogsOutputBytes is the maximum response size for log queries (5MB, matching PeerTube).
const maxLogsOutputBytes = 5 * 1024 * 1024

// logsLevel defines numeric ordering for log levels (lower = more verbose).
var logsLevel = map[string]int{
	"audit": -1,
	"debug": 0,
	"info":  1,
	"warn":  2,
	"error": 3,
}

// LogHandlers handles server log endpoints.
// Logs are read from files (no database) — matching PeerTube's file-based approach.
type LogHandlers struct {
	logDir           string
	logFilename      string
	auditLogFilename string
	acceptClientLog  bool
	logger           *slog.Logger
}

// NewLogHandlers returns a new LogHandlers.
func NewLogHandlers(logDir, logFilename, auditLogFilename string, acceptClientLog bool) *LogHandlers {
	return &LogHandlers{
		logDir:           logDir,
		logFilename:      logFilename,
		auditLogFilename: auditLogFilename,
		acceptClientLog:  acceptClientLog,
		logger:           slog.Default(),
	}
}

// GetServerLogs handles GET /api/v1/server/logs.
// Query params: startDate (required), endDate (optional), level (optional), tagsOneOf (optional, CSV).
func (h *LogHandlers) GetServerLogs(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.UserRoleKey).(string)
	if role != "admin" {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Admin access required"))
		return
	}

	startDate, endDate, level, tagsOneOf, ok := parseLogQueryParams(w, r)
	if !ok {
		return
	}

	if h.logDir == "" {
		// No log directory configured — return empty
		shared.WriteJSON(w, http.StatusOK, []interface{}{})
		return
	}

	output, err := readLogsFromDir(h.logDir, h.logFilename, startDate, endDate, level, tagsOneOf)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to read logs"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, output)
}

// GetAuditLogs handles GET /api/v1/server/audit-logs.
// Query params: startDate (required), endDate (optional).
func (h *LogHandlers) GetAuditLogs(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.UserRoleKey).(string)
	if role != "admin" {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Admin access required"))
		return
	}

	startDate, endDate, _, _, ok := parseLogQueryParams(w, r)
	if !ok {
		return
	}

	if h.logDir == "" {
		shared.WriteJSON(w, http.StatusOK, []interface{}{})
		return
	}

	// Audit log entries have no 'level' field. Pass filterLevel=-2 to skip level filtering entirely.
	output, err := readLogsFromDir(h.logDir, h.auditLogFilename, startDate, endDate, "skip", nil)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to read audit logs"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, output)
}

type createClientLogRequest struct {
	Level   string `json:"level"`
	Message string `json:"message"`
	Meta    string `json:"meta"`
}

// CreateClientLog handles POST /api/v1/server/logs/client.
func (h *LogHandlers) CreateClientLog(w http.ResponseWriter, r *http.Request) {
	if !h.acceptClientLog {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Client log submission is disabled"))
		return
	}

	var req createClientLogRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid request body"))
		return
	}

	if req.Message == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Message is required"))
		return
	}

	if req.Level == "" {
		req.Level = "info"
	}

	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[req.Level] {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid log level"))
		return
	}

	// Log the client message to the server log at the requested level (matching PeerTube's behavior)
	userAgent := r.Header.Get("User-Agent")
	switch req.Level {
	case "debug":
		h.logger.Debug("Client log: "+req.Message, "source", "client", "user_agent", userAgent)
	case "warn":
		h.logger.Warn("Client log: "+req.Message, "source", "client", "user_agent", userAgent)
	case "error":
		h.logger.Error("Client log: "+req.Message, "source", "client", "user_agent", userAgent)
	default:
		h.logger.Info("Client log: "+req.Message, "source", "client", "user_agent", userAgent)
	}

	w.WriteHeader(http.StatusNoContent)
}

// --- Internal log reading helpers ---

// parseLogQueryParams extracts and validates log query parameters.
func parseLogQueryParams(w http.ResponseWriter, r *http.Request) (startDate, endDate time.Time, level string, tagsOneOf map[string]bool, ok bool) {
	startStr := r.URL.Query().Get("startDate")
	if startStr == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "startDate is required"))
		return
	}
	var err error
	startDate, err = time.Parse(time.RFC3339, startStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "startDate must be ISO 8601 format"))
		return
	}

	endStr := r.URL.Query().Get("endDate")
	if endStr != "" {
		endDate, err = time.Parse(time.RFC3339, endStr)
		if err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "endDate must be ISO 8601 format"))
			return
		}
	} else {
		endDate = time.Now()
	}

	level = r.URL.Query().Get("level")
	if level == "" {
		level = "info"
	}

	if tagsStr := r.URL.Query().Get("tagsOneOf"); tagsStr != "" {
		tagsOneOf = make(map[string]bool)
		for _, tag := range strings.Split(tagsStr, ",") {
			tagsOneOf[strings.TrimSpace(tag)] = true
		}
	}

	ok = true
	return
}

// readLogsFromDir reads JSONL log files from the given directory, filtering by date/level/tags.
// Files are sorted by modification time (newest first). Each file is read in reverse to return
// recent entries first, up to maxLogsOutputBytes.
func readLogsFromDir(logDir, baseFilename string, startDate, endDate time.Time, levelFilter string, tagsOneOf map[string]bool) ([]map[string]interface{}, error) {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []map[string]interface{}{}, nil
		}
		return nil, err
	}

	// Build a nameFilter regex pattern: match baseFilename with optional numeric suffixes
	baseName := strings.TrimSuffix(baseFilename, ".log")
	var matchingFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == baseFilename || (strings.HasPrefix(name, baseName) && strings.HasSuffix(name, ".log")) {
			matchingFiles = append(matchingFiles, filepath.Join(logDir, name))
		}
	}

	// Sort by mtime descending (newest first)
	sort.Slice(matchingFiles, func(i, j int) bool {
		si, _ := os.Stat(matchingFiles[i])
		sj, _ := os.Stat(matchingFiles[j])
		if si == nil || sj == nil {
			return false
		}
		return si.ModTime().After(sj.ModTime())
	})

	// "skip" means no level filtering (used for audit logs which have no 'level' field)
	filterLevel := -2
	if levelFilter != "skip" {
		filterLevel = logsLevel[levelFilter]
	}
	var output []map[string]interface{}
	var currentSize int

	startTime := startDate.UnixMilli()
	endTime := endDate.UnixMilli()

	for _, path := range matchingFiles {
		fileOutput, done, err := readFileReverse(path, startTime, endTime, filterLevel, tagsOneOf, &currentSize)
		if err != nil {
			continue
		}
		// Prepend (file output is already chronological within each file)
		output = append(fileOutput, output...)
		if done {
			break
		}
	}

	return output, nil
}

// readFileReverse reads a JSONL log file from end to start using a buffered scanner
// with an enlarged token buffer (1 MB) to handle long log lines. Returns collected
// entries in chronological order and whether to stop processing further files.
func readFileReverse(path string, startTime, endTime int64, filterLevel int, tagsOneOf map[string]bool, currentSize *int) ([]map[string]interface{}, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	// Scan all lines with a 1 MB token buffer to handle large log lines
	// (e.g. stack traces, long comment bodies, JSON payloads)
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1*1024*1024), 1*1024*1024)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			lines = append(lines, line)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, false, err
	}

	var output []map[string]interface{}
	var earliestSeen int64 // tracks the oldest entry timestamp encountered

	// Process in reverse for recency (newest → oldest)
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		// Parse timestamp
		tsStr, _ := entry["timestamp"].(string)
		ts, err := time.Parse(time.RFC3339Nano, tsStr)
		if err != nil {
			ts, err = time.Parse(time.RFC3339, tsStr)
			if err != nil {
				continue
			}
		}
		logTime := ts.UnixMilli()
		earliestSeen = logTime // always update; after loop holds oldest scanned entry

		// Stop early when we've gone past startDate (entries are older than requested)
		if logTime < startTime {
			break
		}

		// Skip entries after endDate
		if logTime > endTime {
			continue
		}

		// Filter by level (skipLevelFilter when filterLevel is -2)
		if filterLevel > -2 {
			entryLevel, _ := entry["level"].(string)
			entryLevelNum := logsLevel[strings.ToLower(entryLevel)]
			if entryLevelNum < filterLevel {
				continue
			}
		}

		// Filter by tags
		if tagsOneOf != nil && !entryHasTag(entry, tagsOneOf) {
			continue
		}

		output = append([]map[string]interface{}{entry}, output...) // prepend for chrono order
		*currentSize += len(line)
		if *currentSize > maxLogsOutputBytes {
			return output, true, nil
		}
	}

	done := earliestSeen > 0 && earliestSeen < startTime
	return output, done, nil
}

// entryHasTag returns true if the log entry's tags field contains any tag in tagsOneOf.
func entryHasTag(entry map[string]interface{}, tagsOneOf map[string]bool) bool {
	tags, ok := entry["tags"]
	if !ok {
		return false
	}
	switch v := tags.(type) {
	case []interface{}:
		for _, tag := range v {
			if s, ok := tag.(string); ok && tagsOneOf[s] {
				return true
			}
		}
	case string:
		return tagsOneOf[v]
	}
	return false
}
