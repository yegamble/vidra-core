// Package blocklistsub syncs shared fediverse denylists (Mastodon/Oliphant CSV,
// plain domain lists, JSON) into the instance blocklist. A subscription owns the
// blocks it creates (blocked_instances.source = "subscription:<id>"); it never
// adds to, changes or removes a manual block, and an admin allowlist wins over
// every list.
package blocklistsub

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
)

// Wire formats.
const (
	FormatMastodonCSV = "mastodon_csv"
	FormatDomainList  = "domain_list"
	FormatJSON        = "json"
)

// ValidFormat reports whether f is a supported format.
func ValidFormat(f string) bool {
	return f == FormatMastodonCSV || f == FormatDomainList || f == FormatJSON
}

// maxEntries bounds how many lines/objects one list may contribute, so a hostile
// or runaway list cannot turn into an unbounded insert.
const maxEntries = 200000

// ErrUnparseable means the body is not the declared format at all.
var ErrUnparseable = errors.New("blocklistsub: body is not a valid list in the declared format")

// Entry is one parsed list row.
type Entry struct {
	Domain   string
	Severity string // lowercased; "" means the format carries none (treated as suspend)
}

// Parsed is the outcome of parsing a list.
type Parsed struct {
	// Block holds the deduplicated domains the list asks to be blocked
	// (severity suspend, or no severity).
	Block []string
	// IgnoredSeverity counts valid rows whose severity vidra has no equivalent
	// for (silence/limit/noop/...): vidra has only a full instance block.
	IgnoredSeverity int
	// Invalid counts rows skipped as malformed (obfuscated, wildcard, IP, ...).
	Invalid int
}

var labelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// cleanDomain validates a list domain strictly: a lowercase DNS name with at
// least two labels and no port, wildcard, scheme, path or IP literal. Wildcards
// and obfuscated names (`exa***.com`) are refused rather than guessed at: blocking
// a broader or different name than the list meant is worse than skipping a row.
func cleanDomain(raw string) (string, bool) {
	d := strings.ToLower(strings.TrimSpace(strings.Trim(strings.TrimSpace(raw), `"'`)))
	d = strings.TrimSuffix(d, ".")
	if d == "" || len(d) > 253 || !strings.Contains(d, ".") {
		return "", false
	}
	labels := strings.Split(d, ".")
	for _, l := range labels {
		if !labelRE.MatchString(l) {
			return "", false
		}
	}
	// A purely numeric TLD means an IPv4 literal (or junk), never a fediverse host.
	tld := labels[len(labels)-1]
	if strings.Trim(tld, "0123456789") == "" {
		return "", false
	}
	return d, true
}

type collector struct {
	seen map[string]bool
	p    Parsed
	rows int
}

func (c *collector) add(domain, severity string) {
	c.rows++
	d, ok := cleanDomain(domain)
	if !ok {
		c.p.Invalid++
		return
	}
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "", "suspend":
	default:
		c.p.IgnoredSeverity++
		return
	}
	if !c.seen[d] {
		c.seen[d] = true
		c.p.Block = append(c.p.Block, d)
	}
}

func (c *collector) full() bool { return c.rows >= maxEntries }

// Parse reads body as format. It is lenient about noise (BOM, CRLF, comments,
// blank lines, short rows, unknown columns) and strict about each domain.
func Parse(format string, body []byte) (Parsed, error) {
	body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
	c := &collector{seen: map[string]bool{}}
	switch format {
	case FormatDomainList:
		parseDomainList(c, body)
	case FormatMastodonCSV:
		if err := parseCSV(c, body); err != nil {
			return Parsed{}, err
		}
	case FormatJSON:
		if err := parseJSON(c, body); err != nil {
			return Parsed{}, err
		}
	default:
		return Parsed{}, ErrUnparseable
	}
	return c.p, nil
}

func parseDomainList(c *collector, body []byte) {
	for _, line := range strings.Split(string(body), "\n") {
		if c.full() {
			return
		}
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		c.add(f[0], "")
	}
}

func parseCSV(c *collector, body []byte) error {
	r := csv.NewReader(bytes.NewReader(body))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	r.TrimLeadingSpace = true
	r.Comment = 0
	domainCol, sevCol := 0, 1
	first := true
	for !c.full() {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			// A malformed row is noise; csv.Reader keeps going after ParseError.
			c.rows++
			c.p.Invalid++
			continue
		}
		if len(rec) == 0 || (len(rec) == 1 && strings.TrimSpace(rec[0]) == "") {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(rec[0]), "#") && !(first && headerHas(rec, "domain")) {
			continue // comment line
		}
		if first && headerHas(rec, "domain") {
			first = false
			domainCol, sevCol = -1, -1
			for i, h := range rec {
				switch strings.ToLower(strings.TrimLeft(strings.TrimSpace(h), "#")) {
				case "domain":
					domainCol = i
				case "severity":
					sevCol = i
				}
			}
			continue
		}
		first = false
		if domainCol < 0 || domainCol >= len(rec) {
			c.rows++
			c.p.Invalid++
			continue
		}
		sev := ""
		if sevCol >= 0 && sevCol < len(rec) {
			sev = rec[sevCol]
		}
		c.add(rec[domainCol], sev)
	}
	return nil
}

func headerHas(rec []string, name string) bool {
	for _, h := range rec {
		if strings.ToLower(strings.TrimLeft(strings.TrimSpace(h), "#")) == name {
			return true
		}
	}
	return false
}

func parseJSON(c *collector, body []byte) error {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return ErrUnparseable
	}
	// An envelope object: take its first array-valued well-known member.
	if m, ok := root.(map[string]any); ok {
		root = nil
		for _, k := range []string{"domains", "instances", "blocks", "data", "items", "domain_blocks"} {
			if a, ok := m[k].([]any); ok {
				root = a
				break
			}
		}
	}
	arr, ok := root.([]any)
	if !ok {
		return ErrUnparseable
	}
	for _, it := range arr {
		if c.full() {
			return nil
		}
		switch v := it.(type) {
		case string:
			c.add(v, "")
		case map[string]any:
			d := ""
			for _, k := range []string{"domain", "host", "instance", "name"} {
				if s, ok := v[k].(string); ok {
					d = s
					break
				}
			}
			sev, _ := v["severity"].(string)
			c.add(d, sev)
		default:
			c.rows++
			c.p.Invalid++
		}
	}
	return nil
}
