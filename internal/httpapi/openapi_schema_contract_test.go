package httpapi

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestInstanceFeaturesSchemaContract closes the components.schemas blind spot in
// TestOpenAPIContract.
//
// That guard's parser gates on `inPaths = trimmed == "paths:"`, so everything
// outside the paths block — every schema in components — is invisible to it. It
// pairs ROUTES with PATHS and nothing else, which left the shape most likely to
// drift silently completely unguarded: GET /api/v1/instance's `features` object
// and the instanceFeatures struct that populates it are two hand-maintained
// lists of the same flags, and nothing compared them. Add a flag to one, forget
// the other, and `make ci` stays green.
//
// It happened. PR #145 added `messaging` and `messaging_e2ee` to the schema's
// `properties` but not to its `required` array, unlike all 17 flags before
// them, so the frontend's generated client typed two always-present booleans as
// optional. This guard is what would have caught it.
//
// Three assertions, all of them consequences of one fact — instanceFeatures is
// marshalled whole, with no omitempty on any field, so the server always sends
// every flag:
//
//  1. every property the schema declares has a Go field behind it (a documented
//     flag the server never sends is a lie the client will branch on);
//  2. every Go field is declared in the schema (an undocumented flag is one the
//     frontend cannot see at all — the drift that hides a new feature gate);
//  3. `required` lists exactly those properties. Anything less makes a client
//     write a fallback for a value that cannot be absent, and that fallback is
//     where a wrong default silently becomes product behaviour.
func TestInstanceFeaturesSchemaContract(t *testing.T) {
	specPath := filepath.Join("..", "..", "api", "openapi.yaml")
	props, required := instanceObjectSchema(t, specPath, "features")
	assertObjectSchemaContract(t, "features", "instanceFeatures", props, required, jsonFieldNames(t, instanceFeatures{}))
}

// TestInstanceBrandingSchemaContract applies the same three-way check to
// InstanceResponse's `branding` object, for the same reason and one more.
//
// branding is the second hand-maintained pair of lists in this document (the Go
// struct and the schema), and it is now the white-label surface: the frontend
// decides whether to render the software's name at all from
// branding.hide_software_name. A branding field the server sends but the spec
// omits is a gate the generated client cannot see, which shows the name on an
// instance that asked for it to be hidden — the exact failure this guard exists
// to make impossible for the NEXT field as well.
//
// It reuses the features guard's parser rather than copying it; the parser reads
// `required` in both the dash-list form features uses and the inline
// `[a, b]` form branding uses, so neither can go vacuous by being reformatted.
func TestInstanceBrandingSchemaContract(t *testing.T) {
	specPath := filepath.Join("..", "..", "api", "openapi.yaml")
	props, required := instanceObjectSchema(t, specPath, "branding")
	assertObjectSchemaContract(t, "branding", "instanceBranding", props, required, jsonFieldNames(t, instanceBranding{}))
}

// assertObjectSchemaContract is the three-way comparison both guards above make:
// schema properties <-> Go fields <-> `required`. object is the InstanceResponse
// key being checked and goType the struct that populates it, so a failure names
// the two files that have drifted.
func assertObjectSchemaContract(t *testing.T, object, goType string, props, required, fields map[string]bool) {
	t.Helper()
	for name := range props {
		if !fields[name] {
			t.Errorf("api/openapi.yaml documents %s.%s but %s has no such field — the server never sends it; remove it from the schema or add the field", object, name, goType)
		}
	}
	for name := range fields {
		if !props[name] {
			t.Errorf("%s sends %q but the InstanceResponse `%s` schema does not declare it — document it in the same change, or the frontend's generated client cannot see it at all", goType, name, object)
		}
	}
	for name := range props {
		if !required[name] {
			t.Errorf("the `%s` schema declares %q but omits it from `required` — %s has no omitempty, so the server ALWAYS sends it and a generated client would wrongly type it optional", object, name, goType)
		}
	}
	for name := range required {
		if !props[name] {
			t.Errorf("the `%s` schema lists %q in `required` but declares no such property", object, name)
		}
	}
}

// jsonFieldNames returns the JSON names a struct marshals, and fails if any
// field carries omitempty — the whole "required must equal properties" rule
// above rests on every field being unconditionally present on the wire, so a
// new omitempty must force a decision here rather than quietly weakening it.
func jsonFieldNames(t *testing.T, v any) map[string]bool {
	t.Helper()
	typ := reflect.TypeOf(v)
	names := map[string]bool{}
	for i := range typ.NumField() {
		tag := typ.Field(i).Tag.Get("json")
		name, opts, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			t.Fatalf("%s.%s has no json tag — this guard compares wire names", typ.Name(), typ.Field(i).Name)
		}
		if strings.Contains(opts, "omitempty") {
			t.Fatalf("%s.%s is omitempty; this guard asserts every documented feature flag is `required`, which is only sound while the struct is marshalled whole", typ.Name(), typ.Field(i).Name)
		}
		names[name] = true
	}
	return names
}

// instanceObjectSchema parses one named object of the InstanceResponse schema
// (`features`, `branding`, …) out of api/openapi.yaml by indentation — the same
// dependency-free approach declaredOperations uses on the paths block, applied to
// the half of the document it never reads. It returns that object's declared
// property names and its `required` entries, ignoring anything nested deeper
// (branding.logos and its own required list, for one).
//
// The indents are read from the file rather than hardcoded, and BOTH spellings of
// `required` are understood: the dash list `features` uses and the inline
// `[a, b]` flow sequence `branding` uses. Anything else — the block moved,
// renamed or reshaped — empties the result and trips the fatal below rather than
// passing vacuously.
func instanceObjectSchema(t *testing.T, specPath, object string) (props, required map[string]bool) {
	t.Helper()
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read OpenAPI spec at %s: %v", specPath, err)
	}

	props, required = map[string]bool{}, map[string]bool{}
	inSchema := false // inside components.schemas.InstanceResponse
	base := -1        // indent of the `features:` key; -1 until found
	section := ""     // which of its child keys we are under
	for raw := range strings.SplitSeq(string(data), "\n") {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent <= 4 && strings.HasSuffix(trimmed, ":") {
			// A schema name (indent 4) or a top-level key: either way the
			// InstanceResponse block, and any features block inside it, ends here.
			inSchema, base, section = trimmed == "InstanceResponse:", -1, ""
			continue
		}
		if !inSchema {
			continue
		}
		if base < 0 {
			if trimmed == object+":" {
				base = indent
			}
			continue
		}
		switch {
		case indent <= base:
			base, section = -1, "" // the object's block ended
		case indent == base+2:
			key, rest, _ := strings.Cut(trimmed, ":")
			section = key
			// The inline form: `required: [avatar, banner, …]`. The dash-list
			// form leaves rest empty and is collected by the case below.
			if key == "required" {
				for name := range strings.SplitSeq(strings.Trim(strings.TrimSpace(rest), "[]"), ",") {
					if name = strings.TrimSpace(name); name != "" {
						required[name] = true
					}
				}
			}
		case indent == base+4 && section == "required":
			required[strings.TrimPrefix(trimmed, "- ")] = true
		case indent == base+4 && section == "properties" && strings.HasSuffix(trimmed, ":"):
			props[strings.TrimSuffix(trimmed, ":")] = true
		}
	}
	if len(props) == 0 || len(required) == 0 {
		t.Fatalf("parsed %d properties and %d required entries from the InstanceResponse `%s` schema in %s — the block moved or was reshaped; fix this parser rather than leaving the guard vacuous", len(props), len(required), object, specPath)
	}
	return props, required
}
