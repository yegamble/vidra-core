package auth

// Lower the bcrypt work factor for the auth package's own tests so the many
// hash/verify round trips stay fast. Production strength (see password.go) is
// unaffected.
func init() { UseFastPasswordHashingForTests() }
