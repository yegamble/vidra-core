package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/port"
)

// ActivityPubDeliveryWorker handles background delivery of ActivityPub activities
type ActivityPubDeliveryWorker struct {
	apRepo  port.ActivityPubRepository
	service port.ActivityPubService
	cfg     *config.Config
	stopCh  chan struct{}
}

// NewActivityPubDeliveryWorker creates a new delivery worker
func NewActivityPubDeliveryWorker(
	apRepo port.ActivityPubRepository,
	service port.ActivityPubService,
	cfg *config.Config,
) *ActivityPubDeliveryWorker {
	return &ActivityPubDeliveryWorker{
		apRepo:  apRepo,
		service: service,
		cfg:     cfg,
		stopCh:  make(chan struct{}),
	}
}

// Start starts the delivery worker
func (w *ActivityPubDeliveryWorker) Start(ctx context.Context) error {
	log.Printf("Starting ActivityPub delivery worker with %d workers", w.cfg.ActivityPubDeliveryWorkers)

	// Start multiple worker goroutines
	for i := 0; i < w.cfg.ActivityPubDeliveryWorkers; i++ {
		go w.deliveryLoop(ctx, i)
	}

	return nil
}

// Stop stops the delivery worker
func (w *ActivityPubDeliveryWorker) Stop() error {
	log.Println("Stopping ActivityPub delivery worker")
	close(w.stopCh)
	return nil
}

// deliveryLoop is the main loop for processing deliveries
func (w *ActivityPubDeliveryWorker) deliveryLoop(ctx context.Context, workerID int) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	log.Printf("ActivityPub delivery worker %d started", workerID)

	for {
		select {
		case <-ctx.Done():
			log.Printf("ActivityPub delivery worker %d stopped (context cancelled)", workerID)
			return
		case <-w.stopCh:
			log.Printf("ActivityPub delivery worker %d stopped (stop signal)", workerID)
			return
		case <-ticker.C:
			w.processDeliveries(ctx, workerID)
		}
	}
}

// processDeliveries processes pending deliveries
func (w *ActivityPubDeliveryWorker) processDeliveries(ctx context.Context, workerID int) {
	// Get pending deliveries
	deliveries, err := w.apRepo.GetPendingDeliveries(ctx, 10)
	if err != nil {
		log.Printf("Worker %d: failed to get pending deliveries: %v", workerID, err)
		return
	}

	if len(deliveries) == 0 {
		return
	}

	log.Printf("Worker %d: processing %d deliveries", workerID, len(deliveries))

	for _, delivery := range deliveries {
		// Mark as processing
		err := w.apRepo.UpdateDeliveryStatus(ctx, port.DeliveryStatusParams{
			DeliveryID: delivery.ID, Status: "processing", Attempts: delivery.Attempts, NextAttempt: delivery.NextAttempt,
		})
		if err != nil {
			log.Printf("Worker %d: failed to update delivery status: %v", workerID, err)
			continue
		}

		// Attempt delivery
		err = w.attemptDelivery(ctx, delivery, workerID)
		if err != nil {
			log.Printf("Worker %d: delivery %s failed (attempt %d/%d): %v",
				workerID, delivery.ID, delivery.Attempts+1, delivery.MaxAttempts, err)

			// Update delivery with error
			delivery.Attempts++
			errMsg := err.Error()
			delivery.LastError = &errMsg

			if delivery.Attempts >= delivery.MaxAttempts {
				// Mark as permanently failed
				_ = w.apRepo.UpdateDeliveryStatus(ctx, port.DeliveryStatusParams{
					DeliveryID: delivery.ID, Status: "failed", Attempts: delivery.Attempts, LastError: delivery.LastError, NextAttempt: delivery.NextAttempt,
				})
				log.Printf("Worker %d: delivery %s permanently failed after %d attempts", workerID, delivery.ID, delivery.Attempts)
			} else {
				// Schedule retry with exponential backoff
				nextAttempt := w.calculateNextAttempt(delivery.Attempts)
				_ = w.apRepo.UpdateDeliveryStatus(ctx, port.DeliveryStatusParams{
					DeliveryID: delivery.ID, Status: "pending", Attempts: delivery.Attempts, LastError: delivery.LastError, NextAttempt: nextAttempt,
				})
				log.Printf("Worker %d: delivery %s scheduled for retry at %s", workerID, delivery.ID, nextAttempt)
			}
		} else {
			// Mark as completed
			log.Printf("Worker %d: delivery %s completed successfully", workerID, delivery.ID)
			_ = w.apRepo.UpdateDeliveryStatus(ctx, port.DeliveryStatusParams{
				DeliveryID: delivery.ID, Status: "completed", Attempts: delivery.Attempts + 1, NextAttempt: delivery.NextAttempt,
			})
		}
	}
}

// attemptDelivery attempts to deliver an activity
func (w *ActivityPubDeliveryWorker) attemptDelivery(ctx context.Context, delivery *domain.APDeliveryQueue, workerID int) error {
	// Get the activity
	activity, err := w.apRepo.GetActivity(ctx, delivery.ActivityID)
	if err != nil {
		return fmt.Errorf("failed to get activity: %w", err)
	}

	if activity == nil {
		return fmt.Errorf("activity not found")
	}

	// Parse activity JSON
	var activityObj map[string]interface{}
	if err := json.Unmarshal(activity.ActivityJSON, &activityObj); err != nil {
		return fmt.Errorf("failed to parse activity: %w", err)
	}

	// Deliver using the service
	return w.service.DeliverActivity(ctx, delivery.ActorID, delivery.InboxURL, activityObj)
}

// calculateNextAttempt calculates the next attempt time with exponential backoff
func (w *ActivityPubDeliveryWorker) calculateNextAttempt(attempts int) time.Time {
	// Base delay from config (in seconds)
	baseDelay := time.Duration(w.cfg.ActivityPubDeliveryRetryDelay) * time.Second
	maxDelay := 24 * time.Hour

	// Exponential backoff: baseDelay * 2^attempts
	// Cap attempts to prevent overflow (2^30 seconds is already > 24 hours with 60s base)
	if attempts > 30 {
		attempts = 30
	}

	delay := baseDelay * time.Duration(1<<uint(attempts))

	// Cap at 24 hours
	if delay > maxDelay {
		delay = maxDelay
	}

	return time.Now().Add(delay)
}
