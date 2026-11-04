package hlsmanifest

import (
	"fmt"
	"strings"
)

// Attribute retains both the parsed value and its unchanged name=value text.
type Attribute struct {
	Name, Value, Raw string
	Quoted           bool
}

// ParseAttributes preserves quoted commas and raw fields, rejecting ambiguous
// syntax. URI safety and tag-specific requirements belong to the caller.
func ParseAttributes(list string) ([]Attribute, error) {
	var attributes []Attribute
	seen := map[string]bool{}
	invalid := fmt.Errorf("invalid HLS attribute list")
	for list != "" {
		name, rest, found := strings.Cut(list, "=")
		if !found || name == "" || rest == "" || seen[name] {
			return nil, invalid
		}
		for _, c := range name {
			if !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') && c != '-' {
				return nil, invalid
			}
		}
		seen[name] = true
		attribute := Attribute{Name: name, Quoted: rest[0] == '"'}
		end := strings.IndexByte(rest, ',')
		if attribute.Quoted {
			end = strings.IndexByte(rest[1:], '"')
			if end < 0 {
				return nil, invalid
			}
			end += 2
			attribute.Value = rest[1 : end-1]
		} else {
			if end < 0 {
				end = len(rest)
			}
			attribute.Value = rest[:end]
			if attribute.Value == "" || strings.ContainsAny(attribute.Value, "\"= \t") {
				return nil, invalid
			}
		}
		if strings.ContainsAny(attribute.Value, "\r\n\x00") || (end < len(rest) && rest[end] != ',') {
			return nil, invalid
		}
		attribute.Raw = name + "=" + rest[:end]
		attributes = append(attributes, attribute)
		list = rest[end:]
		if list != "" {
			list = list[1:]
			if list == "" {
				return nil, invalid
			}
		}
	}
	if len(attributes) == 0 {
		return nil, invalid
	}
	return attributes, nil
}
