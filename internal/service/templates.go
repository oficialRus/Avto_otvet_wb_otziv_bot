package service

import (
	"errors"
	"strings"
)

// TemplateEngine stores pre‑defined reply texts and picks the right one
// depending on the star rating of a feedback.
//
//   • rating 1–3 → Bad template
//   • rating 4–5 → Good template
//
// You may later extend this to load multiple templates per category or use
// text/template for interpolation, but for MVP plain strings are enough.

type TemplateEngine struct {
	bad  string // reply for 1–3 ★
	good string // reply for 4–5 ★
}

// NewTemplateEngine trims input texts and validates they are non‑empty.
// It panics if either template is empty, as the service cannot operate
// without them (fail‑fast on startup).
func NewTemplateEngine(bad, good string) *TemplateEngine {
	b := strings.TrimSpace(bad)
	g := strings.TrimSpace(good)

	if b == "" || g == "" {
		panic(errors.New("template texts must be non‑empty"))
	}
	return &TemplateEngine{
		bad:  b,
		good: g,
	}
}

// Select returns the template suitable for the given rating.
// For any rating <4 returns bad; rating >=4 returns good.
// Out‑of‑range ratings (<1 or >5) are clamped to nearest bucket.
func (t *TemplateEngine) Select(rating int) string {
	if rating >= 4 {
		return t.good
	}
	return t.bad
}
