// Package strutil provides string utility functions for the Vidra Core project.
// This package has no dependencies on internal packages and can be safely
// imported by any layer of the application.
package strutil

import (
	"database/sql"
	"strings"
)

// NullStringToPtr converts a sql.NullString to a *string.
// Returns nil if the NullString is not valid.
func NullStringToPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	return &ns.String
}

// PtrToNullString converts a *string to a sql.NullString.
// Returns a NullString with Valid=false if the pointer is nil.
func PtrToNullString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: *s, Valid: true}
}

// StringPtr returns a pointer to the given string.
// Useful for creating string pointers inline.
func StringPtr(s string) *string {
	return &s
}

// StringValue returns the value of a string pointer,
// or an empty string if the pointer is nil.
func StringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// TruncateWithEllipsis truncates a string to maxLen characters,
// appending "..." if truncation occurs.
func TruncateWithEllipsis(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// NormalizeWhitespace replaces all sequences of whitespace
// characters with a single space and trims leading/trailing spaces.
func NormalizeWhitespace(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

// Contains checks if a string is present in a slice of strings.
func Contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// ContainsAny checks if any of the items are present in the slice.
func ContainsAny(slice []string, items ...string) bool {
	for _, item := range items {
		if Contains(slice, item) {
			return true
		}
	}
	return false
}

// Filter returns a new slice containing only elements that satisfy the predicate.
func Filter(slice []string, predicate func(string) bool) []string {
	result := make([]string, 0, len(slice))
	for _, s := range slice {
		if predicate(s) {
			result = append(result, s)
		}
	}
	return result
}

// Map applies a function to each element of a slice and returns a new slice.
func Map(slice []string, fn func(string) string) []string {
	result := make([]string, len(slice))
	for i, s := range slice {
		result[i] = fn(s)
	}
	return result
}

// TrimNonEmpty returns a slice of non-empty strings with whitespace trimmed.
func TrimNonEmpty(slice []string) []string {
	return Filter(slice, func(s string) bool {
		return strings.TrimSpace(s) != ""
	})
}
