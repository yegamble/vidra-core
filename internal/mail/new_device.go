package mail

import (
	"context"
	"time"
)

// NewDeviceSignIn is the content of a new-device sign-in notice. It carries a
// coarse browser/OS label and the time only: no IP address (core stores none),
// no location, and nothing that could act as a credential.
type NewDeviceSignIn struct {
	When    time.Time
	Browser string
	OS      string
}

// SendNewDeviceSignIn tells an account that a sign-in just succeeded from a
// browser or device it has not been seen on before. Like SendPasswordChanged it
// is an after-the-fact notice with no token and no link that could change
// anything.
func (s *Composer) SendNewDeviceSignIn(ctx context.Context, email string, n NewDeviceSignIn) error {
	subject := "New sign-in to your " + s.cfg.InstanceName + " account"
	body := "Hi,\n\n" +
		"Your " + s.cfg.InstanceName + " account was just signed in to from a browser or device " +
		"we have not seen before.\n\n" +
		"    When:    " + n.When.UTC().Format("2006-01-02 15:04 UTC") + "\n" +
		"    Browser: " + n.Browser + "\n" +
		"    System:  " + n.OS + "\n\n" +
		"If that was you, there is nothing to do.\n\n" +
		"If it was NOT you, someone else may have your password: use \"Forgot password\" to " +
		"take the account back, then sign out of all devices from your security settings.\n\n" +
		"You can turn these notices off in your security settings.\n"
	return s.send(ctx, email, "", subject, body)
}
