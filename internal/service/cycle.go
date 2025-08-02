package service

import (
	"context"
	"time"

	"feedback_bot/internal/storage"

	"feedback_bot/internal/wbapi"

	"go.uber.org/zap"
)

// Service ties together WB API client, storage and templates.
// It is safe for use by multiple goroutines; internal methods are stateless
// except for IO operations delegated to thread‑safe dependencies.

type Service struct {
	client    *wbapi.Client
	store     storage.Store
	templates *TemplateEngine
	log       *zap.SugaredLogger
	take      int // maximum items per fetch (<=5000)
}

// New constructs a Service instance. `take` defines the slice size for the
// WB API call; set to 5000 for maximal coverage.
func New(client *wbapi.Client, store storage.Store, badTpl, goodTpl string, logger *zap.SugaredLogger, take int) *Service {
	if take <= 0 || take > 5000 {
		take = 5000
	}
	if logger == nil {
		logger = zap.NewNop().Sugar()
	}
	return &Service{
		client:    client,
		store:     store,
		templates: NewTemplateEngine(badTpl, goodTpl),
		log:       logger,
		take:      take,
	}
}

// HandleCycle performs a single polling cycle:
//  1. Fetch unanswered feedbacks from WB API.
//  2. For each feedback not yet processed locally:
//     – choose reply template
//     – POST answer
//     – persist ID to storage (idempotent)
//
// All errors are logged; the function never panics.
func (s *Service) HandleCycle(ctx context.Context) {
	start := time.Now()
	s.log.Debug("cycle: fetching feedbacks")

	feedbacks, err := s.client.FetchUnanswered(ctx, s.take, 0)
	if err != nil {
		s.log.Errorw("cycle: fetch failed", "err", err)
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

		exists, err := s.store.Exists(ctx, fb.ID)
		if err != nil {
			s.log.Warnw("cycle: storage exists err", "id", fb.ID, "err", err)
			continue
		}
		if exists {
			skipped++
			continue
		}

		tpl := s.templates.Select(fb.ProductValuation)
		if err := s.client.AnswerFeedback(ctx, fb.ID, tpl); err != nil {
			s.log.Warnw("cycle: answer failed", "id", fb.ID, "err", err)
			failed++
			continue
		}

		if err := s.store.Save(ctx, fb.ID); err != nil {
			s.log.Warnw("cycle: save failed", "id", fb.ID, "err", err)
		}
		answered++
	}

	s.log.Infow("cycle complete",
		"duration", time.Since(start).String(),
		"answered", answered,
		"skipped", skipped,
		"failed", failed,
		"total", len(feedbacks))
}
