package backup

import (
	"context"
	"log"
	"time"
)

type Scheduler struct {
	manager      *BackupManager
	schedule     string
	retention    int
	stopChan     chan struct{}
	doneChan     chan struct{}
	tickInterval time.Duration
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

	log.Printf("Backup scheduler started (interval: %v, retention: %d)", s.tickInterval, s.retention)

	for {
		select {
		case <-ctx.Done():
			log.Println("Backup scheduler stopped (context canceled)")
			return
		case <-s.stopChan:
			log.Println("Backup scheduler stopped")
			return
		case <-ticker.C:
			s.runBackup(ctx)
		}
	}
}

func (s *Scheduler) runBackup(ctx context.Context) {
	log.Println("Scheduled backup starting...")

	result, err := s.manager.CreateBackup(ctx)
	if err != nil {
		log.Printf("Scheduled backup failed: %v", err)
		return
	}

	log.Printf("Scheduled backup completed: %s (size: %d bytes)", result.BackupPath, result.BytesSize)

	if s.retention > 0 {
		if err := s.applyRetention(ctx); err != nil {
			log.Printf("Failed to apply retention policy: %v", err)
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

	toDelete := len(backups) - s.retention
	for i := 0; i < toDelete; i++ {
		if err := s.manager.Target.Delete(ctx, backups[i].Path); err != nil {
			log.Printf("Failed to delete old backup %s: %v", backups[i].Path, err)
		} else {
			log.Printf("Deleted old backup: %s", backups[i].Path)
		}
	}

	return nil
}
