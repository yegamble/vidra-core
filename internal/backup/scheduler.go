package backup

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync/atomic"
	"time"
)

type Scheduler struct {
	manager      *BackupManager
	schedule     string
	retention    int
	stopChan     chan struct{}
	doneChan     chan struct{}
	tickInterval time.Duration
	running      atomic.Bool
}

func NewScheduler(manager *BackupManager, schedule string, retention int) *Scheduler {
	return &Scheduler{
		manager:      manager,
		schedule:     schedule,
		retention:    retention,
		stopChan:     make(chan struct{}),
		doneChan:     make(chan struct{}),
		tickInterval: 24 * time.Hour,
	}
}

func (s *Scheduler) Start(ctx context.Context) {
	go s.run(ctx)
}

func (s *Scheduler) Stop() {
	close(s.stopChan)
	<-s.doneChan
}

func (s *Scheduler) run(ctx context.Context) {
	defer close(s.doneChan)

	ticker := time.NewTicker(s.tickInterval)
	defer ticker.Stop()

	slog.Info(fmt.Sprintf("Backup scheduler started (interval: %v, retention: %d)", s.tickInterval, s.retention))

	for {
		select {
		case <-ctx.Done():
			slog.Info("Backup scheduler stopped (context canceled)")
			return
		case <-s.stopChan:
			slog.Info("Backup scheduler stopped")
			return
		case <-ticker.C:
			if s.running.CompareAndSwap(false, true) {
				go func() {
					defer s.running.Store(false)
					s.runBackup(ctx)
				}()
			} else {
				slog.Info("Backup scheduler: skipping tick, previous backup still running")
			}
		}
	}
}

func (s *Scheduler) runBackup(ctx context.Context) {
	slog.Info("Scheduled backup starting...")

	result, err := s.manager.CreateBackup(ctx)
	if err != nil {
		slog.Info(fmt.Sprintf("Scheduled backup failed: %v", err))
		return
	}

	slog.Info(fmt.Sprintf("Scheduled backup completed: %s (size: %d bytes)", result.BackupPath, result.BytesSize))

	if s.retention > 0 {
		if err := s.applyRetention(ctx); err != nil {
			slog.Info(fmt.Sprintf("Failed to apply retention policy: %v", err))
		}
	}
}

func (s *Scheduler) applyRetention(ctx context.Context) error {
	backups, err := s.manager.Target.List(ctx, "")
	if err != nil {
		return err
	}

	if len(backups) <= s.retention {
		return nil
	}

	sort.Slice(backups, func(i, j int) bool {
		return backups[i].ModTime.Before(backups[j].ModTime)
	})

	toDelete := len(backups) - s.retention
	for i := 0; i < toDelete; i++ {
		if err := s.manager.Target.Delete(ctx, backups[i].Path); err != nil {
			slog.Info(fmt.Sprintf("Failed to delete old backup %s: %v", backups[i].Path, err))
		} else {
			slog.Info(fmt.Sprintf("Deleted old backup: %s", backups[i].Path))
		}
	}

	return nil
}
