package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/vidra/vidra-core/internal/ipfscontrol"
	"github.com/vidra/vidra-core/internal/leaderlock"
)

func runIPFSControlWorker(ctx context.Context, logger *slog.Logger, svc *ipfscontrol.Service, leader *leaderlock.Elector) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !leader.IsLeader() {
				continue
			}
			if err := svc.Tick(ctx); err != nil {
				logger.Warn("ipfs managed operation observation failed")
			}
		}
	}
}
