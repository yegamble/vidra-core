package payments

import "strings"

// repeatString is a small helper used by tests in this package.
func repeatString(s string, n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(s, n)
}
