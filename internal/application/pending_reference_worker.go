package application

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

const (
	pendingReferenceLeaseSeconds = 30
	pendingReferencePollInterval = 500 * time.Millisecond
)

type PendingReferenceWorker struct {
	service *ProcessWagerService
}

func NewPendingReferenceWorker(
	service *ProcessWagerService,
) *PendingReferenceWorker {
	return &PendingReferenceWorker{
		service: service,
	}
}

func (w *PendingReferenceWorker) Run(
	ctx context.Context,
) error {
	if w == nil || w.service == nil {
		return fmt.Errorf(
			"pending reference worker is not initialized",
		)
	}

	slog.Info("pending reference worker started")

	for {
		select {
		case <-ctx.Done():
			return nil

		default:
		}

		processed, err := w.service.RetryPendingReference(
			ctx,
			pendingReferenceLeaseSeconds,
		)
		if err != nil {
			slog.Error("pending reference failed", "error", err)

			if !waitPendingReferenceWorker(
				ctx,
				pendingReferencePollInterval,
			) {
				return nil
			}

			continue
		}

		if processed {
			// There may be more work available.
			// Try again immediately instead of waiting.
			continue
		}

		if !waitPendingReferenceWorker(
			ctx,
			pendingReferencePollInterval,
		) {
			return nil
		}
	}
}

func waitPendingReferenceWorker(
	ctx context.Context,
	duration time.Duration,
) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false

	case <-timer.C:
		return true
	}
}
