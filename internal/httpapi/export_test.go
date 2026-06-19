package httpapi

import "github.com/vidra/vidra-core/internal/auth"

// The httpapi test suite registers many accounts through the real auth service;
// bcrypt at production cost dominates its runtime. Drop to the minimum cost for
// the test binary only (production never calls this).
func init() { auth.UseFastPasswordHashingForTests() }
