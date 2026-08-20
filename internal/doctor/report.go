package doctor

import (
	"fmt"
	"io"
	"strings"
)

// The glyphs. They are the whole interface most of the time: an operator scans
// the left column and reads only the lines that are not ✓.
const (
	glyphOK   = "✓"
	glyphWarn = "⚠"
	glyphFail = "✗"
)

func glyph(s Status) string {
	switch s {
	case StatusOK:
		return glyphOK
	case StatusFail:
		return glyphFail
	default:
		return glyphWarn
	}
}

// Render writes the report for a terminal: sections, one line per finding, and
// the suggested fix indented under the findings that have one.
//
// The shape is deliberate. A fix is printed ONLY for a ⚠ or ✗, and only when the
// check has one — a suggestion under a passing check is noise, and a report that
// is 90% noise is one nobody reads to the bottom. The summary is last so it is
// what remains on screen after a long run.
func Render(w io.Writer, r Report) {
	fmt.Fprintf(w, "vidra doctor — %s (%s)\n", r.Root, r.EnvFile)
	for _, section := range sortedSections(r.Results) {
		fmt.Fprintf(w, "\n%s\n", section)
		for _, res := range r.Results {
			if res.Section != section {
				continue
			}
			fmt.Fprintf(w, "  %s %s: %s\n", glyph(res.Status), res.Check, res.Detail)
			if res.Status != StatusOK && strings.TrimSpace(res.Fix) != "" {
				fmt.Fprintf(w, "      → %s\n", res.Fix)
			}
		}
	}
	ok, warn, fail := r.Counts()
	fmt.Fprintf(w, "\n%d %s  %d %s  %d %s", ok, glyphOK, warn, glyphWarn, fail, glyphFail)
	if r.Elapsed > 0 {
		fmt.Fprintf(w, "  (%s)", r.Elapsed.Round(1e6))
	}
	fmt.Fprintln(w)
	if fail > 0 {
		// The one sentence that says what the exit code means, so a wrapper script
		// author does not have to guess.
		fmt.Fprintf(w, "%d check(s) found a problem — this deployment needs attention before it is deployed to again.\n", fail)
		return
	}
	if warn > 0 {
		fmt.Fprintln(w, "No failures. The warnings above are either checks that could not run here, or findings that do not stop a deployment.")
		return
	}
	fmt.Fprintln(w, "Everything this command can check from here is in order.")
}
