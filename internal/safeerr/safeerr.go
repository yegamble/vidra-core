// Package safeerr carries a failure reason that is safe to show the person who
// asked for the work.
//
// The queue-backed workers (video import, caption jobs, channel sync) persist
// the cause of a failed attempt into a column the owner reads back — "the video
// no longer exists", "the file is too large". That message must never be a raw
// internal error: those carry the import URL, the Whisper endpoint, a database
// string. So each of those services grew the same private three-liner — a
// `failure` struct, its Error method, and a `failf` constructor — used as a
// marker meaning "this error was deliberately written for a human".
//
// One definition of the marker keeps that contract legible: a worker returning
// safeerr.New has vetted the string; anything else must be funnelled through the
// service's internalf (log the detail, return a generic reason).
package safeerr

// safeError is a failure whose message is client-visible by construction.
type safeError struct{ msg string }

func (e *safeError) Error() string { return e.msg }

// New returns an error carrying msg as a safe, client-visible failure reason.
func New(msg string) error { return &safeError{msg: msg} }
