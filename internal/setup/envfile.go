package setup

import (
	"bytes"
	"fmt"
	"strings"
)

// EnvFile is a parsed env file: the ordered lines exactly as they were read,
// plus an index of the ACTIVE assignments among them.
//
// It exists because the generated file must stay the self-documenting artifact
// the template is. Everything that is not an assignment — the header block, the
// section rules, the `generate:` hints, the DESTRUCTIVE-TO-ROTATE warnings, the
// commented-out managed-database examples — is carried through byte for byte and
// in place, so an operator reading env/production.env still reads the reasoning
// behind every value. A key/value map would throw all of that away.
type EnvFile struct {
	lines []envLine
	// index maps an assignment's key to its position in lines.
	index map[string]int
	// order is the assignment keys in file order (index's keys, sorted by line).
	order []string
}

// envLine is one line of the file. Exactly one of the two forms:
//
//	key != ""  — an active assignment; rendered as key=value with the CURRENT value
//	key == ""  — anything else (comment, blank, commented-out assignment); raw wins
type envLine struct {
	raw   string
	key   string
	value string
}

// ParseEnvFile parses template and generated files alike — the format is the
// same one, which is the point: re-running setup over its own output has to see
// exactly what it wrote.
//
// A line is an ACTIVE assignment only when it starts at column zero with
// KEY=<anything>; a leading '#' or any leading whitespace makes it a comment
// line, carried verbatim and never filled. That is what keeps the template's
// commented-out keys (DATABASE_URL, FEDERATION_KEY_KEK, the OTEL block) opt-in:
// they document a capability, they do not request a value.
//
// The value is the remainder of the line, verbatim — no quote stripping, no
// inline-comment stripping — because `--env-file` hands the raw text to compose
// substitution and this package must not invent a second dialect. Duplicate keys
// are an error rather than last-wins: this file gets REWRITTEN, and silently
// dropping one of two conflicting lines is how an operator loses a value.
func ParseEnvFile(b []byte) (*EnvFile, error) {
	f := &EnvFile{index: map[string]int{}}
	// Split on \n and drop a trailing \r so a CRLF file round-trips as LF; the
	// last empty element after a trailing newline is dropped, and Render puts the
	// final newline back.
	text := strings.TrimSuffix(string(b), "\n")
	if text == "" && len(b) == 0 {
		return f, nil
	}
	for i, raw := range strings.Split(text, "\n") {
		raw = strings.TrimSuffix(raw, "\r")
		key, value, ok := splitAssignment(raw)
		if !ok {
			f.lines = append(f.lines, envLine{raw: raw})
			continue
		}
		if prev, dup := f.index[key]; dup {
			return nil, fmt.Errorf("setup: %s is assigned twice (line %d and line %d); remove one — this file is rewritten in place and a duplicate would silently lose a value", key, prev+1, i+1)
		}
		f.index[key] = len(f.lines)
		f.order = append(f.order, key)
		f.lines = append(f.lines, envLine{raw: raw, key: key, value: value})
	}
	return f, nil
}

// splitAssignment recognises the active-assignment form described on
// ParseEnvFile. The key grammar is the POSIX-ish shell one compose accepts.
func splitAssignment(raw string) (key, value string, ok bool) {
	eq := strings.IndexByte(raw, '=')
	if eq <= 0 {
		return "", "", false
	}
	key = raw[:eq]
	for i, r := range key {
		switch {
		case r == '_':
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return "", "", false
		}
	}
	return key, raw[eq+1:], true
}

// Has reports whether key is an active assignment in the file.
func (f *EnvFile) Has(key string) bool { _, ok := f.index[key]; return ok }

// Value returns the current value of an active assignment.
func (f *EnvFile) Value(key string) (string, bool) {
	i, ok := f.index[key]
	if !ok {
		return "", false
	}
	return f.lines[i].value, true
}

// Keys returns the active assignment keys in file order. Resolution walks this
// order, so secret generation (and therefore the golden fixtures) is
// deterministic.
func (f *EnvFile) Keys() []string { return append([]string(nil), f.order...) }

// Values is the whole file as the environment it describes: every active
// assignment, values verbatim. This is what goes to Check — config.CheckEnv
// ignores keys it does not know, so compose-only variables (VIDRA_CORE_TAG,
// FRONTEND_PORT, POSTGRES_*) ride along harmlessly.
func (f *EnvFile) Values() map[string]string {
	out := make(map[string]string, len(f.order))
	for _, k := range f.order {
		v, _ := f.Value(k)
		out[k] = v
	}
	return out
}

// carriedHeader introduces the block that holds keys the previous env file set
// and the template does not define.
const carriedHeader = `
# ------------------------------------------------------------------------------
# Carried over from the previous env file
# ------------------------------------------------------------------------------
# Keys the previous file assigned that this template does not define. They are
# kept verbatim because dropping one would silently change the deployment: a
# FEDERATION_KEY_KEK that disappears here takes every sealed actor key with it,
# and a DATABASE_URL that disappears re-points the api at the bundled Postgres.
# Move any of them into the sections above if a later template adopts the key.`

// Render re-emits the file with values applied: comments, blank lines and
// ordering untouched, each active assignment carrying values[key], and any
// carried keys appended in their own labelled block. A key absent from values
// keeps the value it was parsed with.
func (f *EnvFile) Render(values map[string]string, carried []string) []byte {
	var buf bytes.Buffer
	for _, ln := range f.lines {
		if ln.key == "" {
			buf.WriteString(ln.raw)
			buf.WriteByte('\n')
			continue
		}
		v, ok := values[ln.key]
		if !ok {
			v = ln.value
		}
		if v == ln.value {
			// Verbatim, so an untouched line cannot be reformatted by this code.
			buf.WriteString(ln.raw)
			buf.WriteByte('\n')
			continue
		}
		buf.WriteString(ln.key)
		buf.WriteByte('=')
		buf.WriteString(v)
		buf.WriteByte('\n')
	}
	if len(carried) > 0 {
		buf.WriteString(carriedHeader)
		buf.WriteByte('\n')
		for _, k := range carried {
			buf.WriteString(k)
			buf.WriteByte('=')
			buf.WriteString(values[k])
			buf.WriteByte('\n')
		}
	}
	return buf.Bytes()
}
