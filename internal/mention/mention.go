// Package mention extracts @user / @user@host mentions from comment bodies.
//
// One parser serves every path that cares — the in-app notification fan-out
// and the outbound ActivityPub Mention tags — so they can never disagree about
// who was mentioned. The rules follow PeerTube: a mention starts at the start
// of the text, after whitespace or after an opening bracket/quote, so an email
// (joe@example.com), a URL path (/@alice) or a mid-word @ is never a mention.
package mention

import "strings"

// MaxPerComment caps how many distinct mentions one comment can raise. A
// comment is at most 2000 characters, so without a cap a single post could
// ping hundreds of accounts.
const MaxPerComment = 10

// Mention is one parsed mention. Username and Host are lower-cased; Host is
// empty for a bare @user.
type Mention struct {
	Username string
	Host     string
}

func isNameChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-'
}

func isHostChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-'
}

// startsMention reports whether a mention may begin after prev.
func startsMention(prev byte) bool {
	switch prev {
	case ' ', '\t', '\n', '\r', '(', '[', '{', '"', '\'':
		return true
	}
	return false
}

// span returns the end of the run of bytes at s[i:] accepted by ok.
func span(s string, i int, ok func(byte) bool) int {
	for i < len(s) && ok(s[i]) {
		i++
	}
	return i
}

// Parse returns the distinct mentions in body, in order of first appearance,
// capped at MaxPerComment. Unknown names are not its concern: resolving them
// to accounts is the caller's job.
func Parse(body string) []Mention {
	var out []Mention
	seen := map[Mention]bool{}
	for i := 0; i < len(body) && len(out) < MaxPerComment; i++ {
		if body[i] != '@' || (i > 0 && !startsMention(body[i-1])) {
			continue
		}
		end := span(body, i+1, isNameChar)
		// A trailing '.' or '-' is sentence punctuation, not part of the name.
		name := strings.TrimRight(body[i+1:end], ".-")
		if name == "" {
			continue
		}
		m := Mention{Username: strings.ToLower(name)}
		next := i + 1 + len(name)
		if next < len(body) && body[next] == '@' {
			hostEnd := span(body, next+1, isHostChar)
			host := strings.TrimRight(body[next+1:hostEnd], ".-")
			// A host needs a dot ("localhost" is just trailing text).
			if strings.Contains(host, ".") {
				m.Host = strings.ToLower(host)
				next += 1 + len(host)
			}
		}
		i = next - 1
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

// LocalUsernames returns the lower-cased usernames of mentions that address an
// account on this instance: a bare @user, or @user@<localHost>.
func LocalUsernames(ms []Mention, localHost string) []string {
	var out []string
	for _, m := range ms {
		if m.Host == "" || strings.EqualFold(m.Host, localHost) {
			out = append(out, m.Username)
		}
	}
	return out
}
