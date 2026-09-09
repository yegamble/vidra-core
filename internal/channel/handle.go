package channel

import "regexp"

// handleRe constrains channel handles to a URL-safe, federation-friendly shape:
// 3–30 characters of letters, digits and underscore.
//
// It lives HERE, in the domain package, rather than beside the HTTP request it
// was born in, because it stopped being an HTTP concern the moment a MIGRATION
// started minting handles. The A29 rehearsal-3 lab measured what that costs
// when the two rules live apart: 0142's backfill renamed a colliding channel to
// `creatora-channel-2`, a name this expression refuses, so the instance handed
// an operator a handle its own create form would not accept and could not be
// re-created. Migration 0143 mints in this alphabet; the integration test that
// exercises the migration's rule asserts the result against THIS function, so
// the two cannot drift apart again without a test going red.
var handleRe = regexp.MustCompile(`^[A-Za-z0-9_]{3,30}$`)

// ValidateChannelHandle reports whether h is a handle this instance will accept
// for a channel. It is deliberately total and unforgiving: it does not trim, and
// it does not lower-case, because both are the caller's decision and a validator
// that silently repaired its input would let a handle differ from the one the
// caller thinks it validated.
func ValidateChannelHandle(h string) bool {
	return handleRe.MatchString(h)
}
