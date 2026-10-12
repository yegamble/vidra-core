package mail

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	netmail "net/mail"
)

func notificationComposer(rec *recordingTransport, base string) *Composer {
	return NewComposer(ComposerConfig{InstanceName: "Vidra Test", PublicBaseURL: base},
		NewStaticResolver(rec, netmail.Address{Address: "no-reply@vidra.test"}, ""))
}

// Each email-capable notification type reads as what happened, names the
// instance, and deep-links to the video and to the settings page that turns it
// off — built from PUBLIC_BASE_URL alone (a trailing slash is not doubled).
func TestNotificationEmailCopyAndLinks(t *testing.T) {
	const videoID = "6f1c1e0e-8a51-4c43-9d4e-0b3f2f0c9a11"
	for _, tc := range []struct {
		n       NotificationEmail
		subject []string
		body    []string
	}{
		{
			n:       NotificationEmail{Type: "new_video", ChannelName: "Ada's Lab", VideoID: videoID, VideoTitle: "Soldering 101"},
			subject: []string{"Ada's Lab", "new video", "Soldering 101"},
			body:    []string{"Ada's Lab published a new video on Vidra Test", "Soldering 101", "new videos from channels you follow"},
		},
		{
			n:       NotificationEmail{Type: "comment", ActorName: "bob", VideoID: videoID, VideoTitle: "Soldering 101"},
			subject: []string{"bob", "commented", "Soldering 101"},
			body:    []string{"bob commented on your video on Vidra Test", "Soldering 101", "comments on your videos"},
		},
		{
			n:       NotificationEmail{Type: "comment_reply", ActorName: "carol", VideoID: videoID, VideoTitle: "Soldering 101"},
			subject: []string{"carol", "replied to your comment"},
			body:    []string{"carol replied to your comment on Vidra Test", "Soldering 101", "replies to your comments"},
		},
	} {
		rec := &recordingTransport{}
		if err := notificationComposer(rec, "https://vidra.example/").SendNotificationEmail(context.Background(), "ada@example.test", tc.n); err != nil {
			t.Fatalf("%s: SendNotificationEmail: %v", tc.n.Type, err)
		}
		if len(rec.msgs) != 1 {
			t.Fatalf("%s: sent %d messages, want 1", tc.n.Type, len(rec.msgs))
		}
		m := rec.msgs[0]
		if m.To != "ada@example.test" {
			t.Errorf("%s: To = %q", tc.n.Type, m.To)
		}
		for _, want := range tc.subject {
			if !strings.Contains(m.Subject, want) {
				t.Errorf("%s: subject %q lacks %q", tc.n.Type, m.Subject, want)
			}
		}
		for _, want := range append(tc.body,
			"https://vidra.example/videos/"+videoID,
			"https://vidra.example/settings/notifications") {
			if !strings.Contains(m.Text, want) {
				t.Errorf("%s: body lacks %q:\n%s", tc.n.Type, want, m.Text)
			}
		}
		if strings.Contains(m.Text, "vidra.example//") {
			t.Errorf("%s: doubled slash in a link:\n%s", tc.n.Type, m.Text)
		}
	}
}

// Without PUBLIC_BASE_URL there is no host to link to, and a link to a guessed
// one is worse than none: the message still says what happened and where to
// turn it off, in words.
func TestNotificationEmailWithoutBaseURLHasNoLinks(t *testing.T) {
	rec := &recordingTransport{}
	n := NotificationEmail{Type: "comment", ActorName: "bob", VideoID: "x", VideoTitle: "Soldering 101"}
	if err := notificationComposer(rec, "").SendNotificationEmail(context.Background(), "ada@example.test", n); err != nil {
		t.Fatalf("SendNotificationEmail: %v", err)
	}
	body := rec.msgs[0].Text
	if strings.Contains(body, "http") || strings.Contains(body, "/videos/") {
		t.Errorf("body links somewhere with no base URL:\n%s", body)
	}
	if !strings.Contains(body, "notification settings") {
		t.Errorf("body does not say how to stop these:\n%s", body)
	}
}

// Titles and names are other people's free text. Each is collapsed to one line
// (a title cannot forge extra lines in the body or a header in the subject)
// and capped, so a 10 KB title cannot become a 10 KB subject.
func TestNotificationEmailFoldsAndCapsUserText(t *testing.T) {
	rec := &recordingTransport{}
	n := NotificationEmail{
		Type: "new_video", ChannelName: "Evil\r\nBcc: victim@example.test",
		VideoID: "x", VideoTitle: "Line one\n\nReset your password at https://evil.example\n" + strings.Repeat("y", 500),
	}
	if err := notificationComposer(rec, "https://vidra.example").SendNotificationEmail(context.Background(), "ada@example.test", n); err != nil {
		t.Fatalf("SendNotificationEmail: %v", err)
	}
	m := rec.msgs[0]
	if strings.ContainsAny(m.Subject, "\r\n") {
		t.Errorf("subject carries a line break: %q", m.Subject)
	}
	for _, line := range strings.Split(m.Text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Reset your password") || strings.HasPrefix(line, "Bcc:") {
			t.Errorf("user text forged its own line: %q", line)
		}
	}
	if utf8.RuneCountInString(m.Subject) > 200 {
		t.Errorf("subject is %d runes, want the title capped", utf8.RuneCountInString(m.Subject))
	}
	if strings.Contains(m.Text, strings.Repeat("y", 200)) {
		t.Error("body carries the uncapped title")
	}
}

// A type with no template is an error, never an empty or generic message; and
// an instance with no mail path sends nothing and reports no error, exactly
// like every other message (the historical no-op contract).
func TestNotificationEmailUnknownTypeAndUnconfigured(t *testing.T) {
	rec := &recordingTransport{}
	if err := notificationComposer(rec, "").SendNotificationEmail(context.Background(), "ada@example.test",
		NotificationEmail{Type: "follow", ActorName: "bob"}); err == nil {
		t.Error("an untemplated type was accepted")
	}
	if len(rec.msgs) != 0 {
		t.Errorf("an untemplated type sent %d messages", len(rec.msgs))
	}
	unconfigured := NewComposer(ComposerConfig{InstanceName: "Vidra Test"}, nil)
	if err := unconfigured.SendNotificationEmail(context.Background(), "ada@example.test",
		NotificationEmail{Type: "comment", ActorName: "bob", VideoID: "x", VideoTitle: "t"}); err != nil {
		t.Errorf("unconfigured instance = %v, want nil", err)
	}
}
