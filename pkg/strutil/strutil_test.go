package strutil

import (
	"database/sql"
	"strings"
	"testing"
)

func TestNullStringToPtr(t *testing.T) {
	tests := []struct {
		name      string
		input     sql.NullString
		wantNil   bool
		wantValue string
	}{
		{
			name:      "valid string",
			input:     sql.NullString{String: "test", Valid: true},
			wantNil:   false,
			wantValue: "test",
		},
		{
			name:    "invalid string",
			input:   sql.NullString{String: "test", Valid: false},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NullStringToPtr(tt.input)
			if tt.wantNil {
				if got != nil {
					t.Errorf("NullStringToPtr() = %v, want nil", got)
				}
			} else {
				if got == nil {
					t.Errorf("NullStringToPtr() = nil, want non-nil")
				} else if *got != tt.wantValue {
					t.Errorf("NullStringToPtr() = %v, want %v", *got, tt.wantValue)
				}
			}
		})
	}
}

func TestPtrToNullString(t *testing.T) {
	testStr := "test"
	tests := []struct {
		name      string
		input     *string
		wantValid bool
		wantValue string
	}{
		{
			name:      "non-nil pointer",
			input:     &testStr,
			wantValid: true,
			wantValue: "test",
		},
		{
			name:      "nil pointer",
			input:     nil,
			wantValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PtrToNullString(tt.input)
			if got.Valid != tt.wantValid {
				t.Errorf("PtrToNullString().Valid = %v, want %v", got.Valid, tt.wantValid)
			}
			if tt.wantValid && got.String != tt.wantValue {
				t.Errorf("PtrToNullString().String = %v, want %v", got.String, tt.wantValue)
			}
		})
	}
}

func TestStringPtr(t *testing.T) {
	s := "test"
	ptr := StringPtr(s)
	if ptr == nil {
		t.Fatal("StringPtr() returned nil")
	}
	if *ptr != s {
		t.Errorf("StringPtr() = %v, want %v", *ptr, s)
	}
}

func TestStringValue(t *testing.T) {
	testStr := "test"
	tests := []struct {
		name  string
		input *string
		want  string
	}{
		{
			name:  "non-nil pointer",
			input: &testStr,
			want:  "test",
		},
		{
			name:  "nil pointer",
			input: nil,
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StringValue(tt.input)
			if got != tt.want {
				t.Errorf("StringValue() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTruncateWithEllipsis(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{
			name:   "no truncation needed",
			input:  "short",
			maxLen: 10,
			want:   "short",
		},
		{
			name:   "truncate with ellipsis",
			input:  "this is a long string",
			maxLen: 10,
			want:   "this is...",
		},
		{
			name:   "very short maxLen",
			input:  "test",
			maxLen: 2,
			want:   "te",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateWithEllipsis(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("TruncateWithEllipsis() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizeWhitespace(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "multiple spaces",
			input: "hello    world",
			want:  "hello world",
		},
		{
			name:  "tabs and newlines",
			input: "hello\t\nworld",
			want:  "hello world",
		},
		{
			name:  "leading and trailing spaces",
			input: "  hello world  ",
			want:  "hello world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeWhitespace(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeWhitespace() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestContains(t *testing.T) {
	slice := []string{"apple", "banana", "cherry"}

	tests := []struct {
		name string
		item string
		want bool
	}{
		{
			name: "item exists",
			item: "banana",
			want: true,
		},
		{
			name: "item does not exist",
			item: "grape",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Contains(slice, tt.item)
			if got != tt.want {
				t.Errorf("Contains() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFilter(t *testing.T) {
	slice := []string{"apple", "banana", "apricot", "cherry"}
	predicate := func(s string) bool {
		return strings.HasPrefix(s, "a")
	}

	got := Filter(slice, predicate)
	want := []string{"apple", "apricot"}

	if len(got) != len(want) {
		t.Errorf("Filter() returned %d items, want %d", len(got), len(want))
	}

	for i, v := range got {
		if v != want[i] {
			t.Errorf("Filter()[%d] = %v, want %v", i, v, want[i])
		}
	}
}

func TestTrimNonEmpty(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "mixed empty and non-empty",
			input: []string{"  ", "hello", "", "world", "   "},
			want:  []string{"hello", "world"},
		},
		{
			name:  "all empty",
			input: []string{"", "  ", "   "},
			want:  []string{},
		},
		{
			name:  "all non-empty",
			input: []string{"hello", "world"},
			want:  []string{"hello", "world"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TrimNonEmpty(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("TrimNonEmpty() returned %d items, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("TrimNonEmpty()[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}
