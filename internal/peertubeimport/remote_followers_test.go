package peertubeimport

import "testing"

// The source's cached actor becomes a federation cache row only when it can be
// delivered to and verified against, and an absent shared inbox stays NULL:
// ListRemoteFollowerInboxes COALESCEs it, so ” would hide the inbox entirely.
func TestSourceRemoteFollowerCachedActor(t *testing.T) {
	good := SourceRemoteFollower{
		ActorURL: "https://Social.example/users/fan", ActorType: "Person", PreferredUsername: "fan",
		Host: "Social.example", InboxURL: "https://social.example/users/fan/inbox",
		SharedInboxURL: "https://social.example/inbox", FollowersURL: "https://social.example/users/fan/followers",
		PublicKeyPEM: "-----BEGIN PUBLIC KEY-----\nx\n-----END PUBLIC KEY-----",
	}
	p, ok := good.CachedActor()
	if !ok {
		t.Fatal("a complete record was refused")
	}
	if p.Domain != "social.example" || p.InboxUrl != good.InboxURL || p.SharedInboxUrl == nil ||
		*p.SharedInboxUrl != good.SharedInboxURL || p.PublicKeyPem != good.PublicKeyPEM || p.AttributedTo != "" {
		t.Errorf("mapped = %+v", p)
	}

	noShared := good
	noShared.SharedInboxURL = ""
	if p, ok := noShared.CachedActor(); !ok || p.SharedInboxUrl != nil {
		t.Errorf("absent shared inbox = %v (ok=%v), want NULL", p.SharedInboxUrl, ok)
	}

	for name, mutate := range map[string]func(*SourceRemoteFollower){
		"no inbox":              func(f *SourceRemoteFollower) { f.InboxURL = "" },
		"no public key":         func(f *SourceRemoteFollower) { f.PublicKeyPEM = "  " },
		"id on another server":  func(f *SourceRemoteFollower) { f.ActorURL = "https://elsewhere.example/users/fan" },
		"id is not http(s)":     func(f *SourceRemoteFollower) { f.ActorURL = "acct:fan@social.example" },
		"inbox is not http(s)":  func(f *SourceRemoteFollower) { f.InboxURL = "javascript:alert(1)" },
		"server host is absent": func(f *SourceRemoteFollower) { f.Host = "" },
	} {
		f := good
		mutate(&f)
		if _, ok := f.CachedActor(); ok {
			t.Errorf("%s: accepted", name)
		}
	}
}
