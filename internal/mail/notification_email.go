package mail

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// NotificationEmail is one in-app notification rendered as an email, for a user
// who turned email on for its type (migration 0157). It carries names and ids
// only, never comment text: the in-app notification stores none, and an email
// may transit third-party relays.
type NotificationEmail struct {
	// Type is the notification type: new_video, comment or comment_reply.
	Type string
	// ActorName is who acted (display name, else username) — the commenter or
	// replier. new_video names the channel instead.
	ActorName   string
	ChannelName string
	VideoID     string
	VideoTitle  string
}

// Frontend pages a notification email links to (see redemptionLink for why the
// mailer is the one place that names pages).
const (
	videoPathPrefix           = "/videos/"
	notificationSettingsPath  = "/settings/notifications"
	notificationTextMaxRunes  = 100
	notificationTextEllipsis  = "…"
	notificationSettingsWords = "your notification settings"
)

// SendNotificationEmail mails one notification: what happened, a deep link to
// the video, and how to stop these. Plain text. Links come from PUBLIC_BASE_URL
// alone — never a request header — and are omitted when it is unset. Other
// people's free text (titles, names) is folded to one capped line so it cannot
// forge lines in the body or headers in the subject. A type with no template is
// an error rather than a generic message.
func (s *Composer) SendNotificationEmail(ctx context.Context, to string, n NotificationEmail) error {
	name := s.effectiveInstanceName()
	title, actor, channel := foldUserText(n.VideoTitle), foldUserText(n.ActorName), foldUserText(n.ChannelName)
	var subject, lead, what string
	switch n.Type {
	case "new_video":
		subject = channel + " published a new video: " + title
		lead = channel + " published a new video on " + name + ":"
		what = "new videos from channels you follow"
	case "comment":
		subject = actor + " commented on your video \"" + title + "\""
		lead = actor + " commented on your video on " + name + ":"
		what = "comments on your videos"
	case "comment_reply":
		subject = actor + " replied to your comment"
		lead = actor + " replied to your comment on " + name + ", on this video:"
		what = "replies to your comments"
	default:
		return fmt.Errorf("mail: no notification email for type %q", n.Type)
	}
	body := "Hi,\n\n" + lead + "\n\n    " + title + "\n"
	settings := notificationSettingsWords
	if base := strings.TrimRight(s.cfg.PublicBaseURL, "/"); base != "" {
		body += "\nWatch it here: " + base + videoPathPrefix + url.PathEscape(n.VideoID) + "\n"
		settings += ": " + base + notificationSettingsPath
	}
	body += "\nYou are receiving this because you turned on email for " + what +
		". To stop, turn it off in " + settings + ".\n"
	return s.send(ctx, to, "", subject, body)
}

// foldUserText collapses every run of whitespace (line breaks included) to one
// space and caps the result, so free text stays a single bounded line.
func foldUserText(v string) string {
	v = strings.Join(strings.Fields(v), " ")
	if r := []rune(v); len(r) > notificationTextMaxRunes {
		v = strings.TrimSpace(string(r[:notificationTextMaxRunes])) + notificationTextEllipsis
	}
	return v
}
