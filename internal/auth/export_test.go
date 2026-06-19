package auth

import "golang.org/x/crypto/bcrypt"

// Lower the bcrypt work factor for the auth package's own tests so the many
// hash/verify round trips stay fast. Production strength (see password.go) is
// unaffected.
func init() { bcryptCost = bcrypt.MinCost }
