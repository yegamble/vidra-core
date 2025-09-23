package scheduler

import "context"

type Scheduler interface {
	Start(ctx context.Context)
	Stop()
}
