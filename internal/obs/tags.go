package obs

import "log/slog"

// LoggerTagsFactory creates a function that returns slog attrs containing a merged tag array.
// Matches PeerTube's loggerTagsFactory pattern for domain-specific log categorization.
//
// Usage:
//
//	lTags := obs.LoggerTagsFactory("ap", "video")
//	logger.Info("processing", lTags("update")...)
//	// produces: {"tags": ["ap", "video", "update"], ...}
func LoggerTagsFactory(defaultTags ...string) func(tags ...string) []any {
	return func(tags ...string) []any {
		all := make([]string, 0, len(defaultTags)+len(tags))
		all = append(all, defaultTags...)
		all = append(all, tags...)
		if len(all) == 0 {
			return nil
		}
		// Pass []string directly to slog.Any — avoids unnecessary []any allocation.
		return []any{slog.Any("tags", all)}
	}
}
