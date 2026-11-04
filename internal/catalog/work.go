package catalog

import (
	"time"

	"github.com/google/uuid"
)

// SourceWork is one immutable delivery observation, never source read readiness.
// END bytes are opaque here; strict envelope preflight precedes delivery.
type SourceWork struct {
	Evidence                 SourceEvidence
	ForceFull                bool
	ForceFullAfterGeneration int64
	LastErrorCode            string
	LastErrorID              *uuid.UUID
	ReceiverPredecessor      *uuid.UUID
	VerifiedAt               *time.Time
	PendingCapturedAt        *time.Time
	PendingCanonicalBytes    int64
	PendingEndRequestBody    []byte
}
