package service

import (
	"context"
	"time"

	"feedback_bot/internal/storage"
	"feedback_bot/internal/wbapi"
	"feedback_bot/pkg/metrics"

	"go.uber.org/zap"
)

// Service ties together Wildberries API client, storage and templates.
// It is safe for use by multiple goroutines; internal methods are stateless
// except for IO operations delegated to thread‑safe dependencies.

type Service struct {
	userID    int64 // user ID for multi-user support
	client    *wbapi.Client
	store     storage.Store
	templates *TemplateEngine
	log       *zap.SugaredLogger
	take      int // maximum items per fetch (<=5000 for WB)
}

// New constructs a Service instance. `take` defines the slice size for the
// API call; set to 5000 for maximal coverage (WB limit).
func New(userID int64, client *wbapi.Client, store storage.Store, badTpl, goodTpl string, logger *zap.SugaredLogger, take int) *Service {
	if take <= 0 || take > 5000 {
		take = 5000
	}
	if logger == nil {
		logger = zap.NewNop().Sugar()
	}
	return &Service{
		userID:    userID,
		client:    client,
		store:     store,
		templates: NewTemplateEngine(badTpl, goodTpl),
		log:       logger,
		take:      take,
	}
}

// HandleCycle performs a single polling cycle:
//  1. Fetch unanswered reviews from Wildberries API.
//  2. For each review not yet processed locally:
//     – choose reply template based on rating
//     – POST answer
//     – persist ID to storage (idempotent)
//
// All errors are logged; the function never panics.
func (s *Service) HandleCycle(ctx context.Context) {
	start := time.Now()
	s.log.Debug("cycle: fetching reviews")

	feedbacks, err := s.client.FetchUnanswered(ctx, s.take, 0)
	if err != nil {
		s.log.Errorw("cycle: fetch failed", "err", err)
		metrics.IncrementAPIError("wb", "fetch")
		return
	}

	var answered, skipped, failed int

	for _, fb := range feedbacks {
		select {
		case <-ctx.Done():
			s.log.Infow("cycle: context cancelled", "answered", answered, "skipped", skipped, "failed", failed)
			return
		default:
		}

		exists, err := s.store.Exists(ctx, s.userID, fb.ID)
		if err != nil {
			s.log.Warnw("cycle: storage exists err", "user_id", s.userID, "id", fb.ID, "err", err)
			metrics.IncrementDatabaseError("exists")
			continue
		}
		if exists {
			skipped++
			continue
		}

		tpl := s.templates.Select(fb.ProductValuation)
		if err := s.client.AnswerFeedback(ctx, fb.ID, tpl); err != nil {
			s.log.Warnw("cycle: answer failed", "user_id", s.userID, "id", fb.ID, "err", err)
			metrics.IncrementAPIError("wb", "answer")
			failed++
			continue
		}

		if err := s.store.Save(ctx, s.userID, fb.ID); err != nil {
			s.log.Warnw("cycle: save failed", "user_id", s.userID, "id", fb.ID, "err", err)
			metrics.IncrementDatabaseError("save")
		} else {
			answered++
			metrics.IncrementProcessedFeedback(s.userID, "answered")
		}
	}

	// Report skipped and failed
	for i := 0; i < skipped; i++ {
		metrics.IncrementProcessedFeedback(s.userID, "skipped")
	}
	for i := 0; i < failed; i++ {
		metrics.IncrementProcessedFeedback(s.userID, "failed")
	}

	s.log.Infow("cycle complete",
		"user_id", s.userID,
		"duration", time.Since(start).String(),
		"answered", answered,
		"skipped", skipped,
		"failed", failed,
		"total", len(feedbacks))
}
