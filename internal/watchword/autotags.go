package watchword

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TagExternalLink is the automatic tag on text that contains a URL (PeerTube
// 6.2 "automatic tags"). The other automatic tags are the names of the INSTANCE
// watched-word lists the text matches.
const TagExternalLink = "external-link"

var urlRE = regexp.MustCompile(`(?i)\b(?:https?://|www\.)\S+`)

// AutomaticTags derives a text's automatic tags from the matches the existing
// matcher already produced — no second matcher. A creator's PRIVATE list
// contributes no tag (its name would reach staff through the tag filter; the
// matcher's list_owner is NULL only for instance lists), and a tag is a list
// NAME only: the matched words never appear in it.
func AutomaticTags(text string, ms []matched) []string {
	seen := map[string]bool{}
	var tags []string
	add := func(t string) {
		if t != "" && !seen[t] {
			seen[t] = true
			tags = append(tags, t)
		}
	}
	if urlRE.MatchString(text) {
		add(TagExternalLink)
	}
	for _, m := range ms {
		if !m.ListOwner.Valid {
			add(strings.TrimSpace(m.ListName))
		}
	}
	sort.Strings(tags)
	return tags
}

// recordCommentTags replaces the comment's automatic tags (best-effort part of
// FlagComment, which computes it from the matches it already holds).
func (s *Service) recordCommentTags(ctx context.Context, commentID uuid.UUID, body string, ms []matched) error {
	tags := AutomaticTags(body, ms)
	if tags == nil {
		tags = []string{}
	}
	return s.repo.ReplaceCommentAutomaticTags(ctx, sqlcgen.ReplaceCommentAutomaticTagsParams{CommentID: commentID, Tags: tags})
}
