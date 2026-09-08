package video

// The safety-scan vocabulary, in one place because four packages have to agree
// on it byte for byte: video (the seam that rejects), uploadfinalize and
// videoimport (the two async pipelines that report the rejection on a session /
// job), and httpapi (the synchronous route and the error envelope). A28 found
// the creator-facing half of this missing entirely — a malware rejection
// settled the upload session as `completed` with an empty failure_reason and
// showed the creator a bare FAILED badge — so the sentence is a constant, not a
// string literal repeated per call site.

// SafetyScanRejectedMessage is the ONE neutral sentence a creator sees when the
// instance's malware scanner refuses their file, on the upload session, on the
// import job, and in the Studio.
//
// It deliberately does NOT name malware, the scanner, or the signature. Telling
// a creator "malware" is telling an attacker their probe worked, and the
// signature name is an oracle for tuning a payload against the instance's
// engine. The verdict is not lost: it stays in the audit row
// (content.upload.malware_rejected) and, under quarantine mode, on the
// moderation-queue entry — surfaces only staff can read.
const SafetyScanRejectedMessage = "This file was rejected by the instance's safety scan and was not stored."

// SafetyScanRejectedCode is the stable machine-readable reason the frontend
// renders the sentence from. The frontend must key on this and never on the
// sentence itself.
const SafetyScanRejectedCode = "safety_scan_rejected"

// MalwareRejectedError is returned by Process alongside the already-persisted
// 'failed' video when the malware scanner refused the bytes. It is a TERMINAL
// outcome, not a transient job failure: the pipelines that receive it must
// dead-letter immediately and write SafetyScanRejectedMessage where the creator
// can read it, rather than retrying a verdict that will never change.
type MalwareRejectedError struct {
	// Outcome is the audit reason class: "infected" (the scanner named a
	// signature) or "scan_error" (the scan could not complete and the policy in
	// force refuses). Never rendered to a creator.
	Outcome string
}

func (e *MalwareRejectedError) Error() string {
	return "video: rejected by the malware scan (" + e.Outcome + ")"
}

// Terminal marks the error as one no retry can clear. The job pipelines test for
// this rather than for the concrete type, so a future terminal rejection (an
// extension gate, a policy refusal) joins the same path without touching them.
func (e *MalwareRejectedError) Terminal() bool { return true }

// TerminalError is the interface uploadfinalize and videoimport test for before
// scheduling a retry.
type TerminalError interface {
	error
	Terminal() bool
}
