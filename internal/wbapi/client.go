package wbapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"time"

	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

// DefaultHTTPTimeout sets the maximum duration of a single request.
const DefaultHTTPTimeout = 15 * time.Second

// Client is a thin wrapper over WB Feedbacks API.
// It handles: auth header, base URL, rate limiting and JSON decoding.
// No retries here — higher layers (retry pkg) decide on backoff strategy.
// All public methods are safe for concurrent use; limiter serialises if needed.
//
// Example:
//
//	cli := wbapi.New(token,
//	    wbapi.WithRateLimit(3, 6),
//	    wbapi.WithLogger(log),
//	)
//	fbs, err := cli.FetchUnanswered(ctx, 5000, 0)
type Client struct {
	httpClient *http.Client
	baseURL    *url.URL
	token      string
	limiter    *rate.Limiter
	log        *zap.SugaredLogger
}

// Option mutates the client during construction.
// Use functional options to avoid breaking callers when adding new fields.
type Option func(*Client)

// WithBaseURL overrides the default API endpoint.
func WithBaseURL(raw string) Option {
	return func(c *Client) {
		if raw == "" {
			return
		}
		u, err := url.Parse(raw)
		if err == nil {
			c.baseURL = u
		}
	}
}

// WithRateLimit sets the per‑second rate and burst size.
// If rps <=0, limiter is disabled.
func WithRateLimit(rps, burst int) Option {
	return func(c *Client) {
		if rps > 0 {
			c.limiter = rate.NewLimiter(rate.Limit(rps), burst)
		}
	}
}

// WithLogger allows injecting custom zap logger. If nil, a no‑op logger will be used.
func WithLogger(l *zap.SugaredLogger) Option {
	return func(c *Client) {
		if l != nil {
			c.log = l
		}
	}
}

// New constructs Client with mandatory token and optional modifiers.
func New(token string, opts ...Option) *Client {
	// sensible defaults
	base, _ := url.Parse("https://feedbacks-api.wildberries.ru")
	c := &Client{
		httpClient: &http.Client{Timeout: DefaultHTTPTimeout},
		baseURL:    base,
		token:      token,
		limiter:    rate.NewLimiter(rate.Inf, 0), // disabled limiter by default
		log:        zap.NewNop().Sugar(),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// FetchUnanswered retrieves a slice of unanswered feedbacks ordered by date desc.
// "take" must be ≤5000 as per API, "skip" may be 0. For MVP we need at most 5000.
func (c *Client) FetchUnanswered(ctx context.Context, take, skip int) ([]Feedback, error) {
	values := url.Values{}
	values.Set("isAnswered", "false")
	values.Set("take", fmt.Sprint(take))
	values.Set("skip", fmt.Sprint(skip))
	values.Set("order", "dateDesc")

	endpoint := c.resolve("/api/v1/feedbacks") + "?" + values.Encode()
	var resp feedbacksListResp
	if err := c.get(ctx, endpoint, &resp); err != nil {
		return nil, err
	}
	if resp.Error {
		return nil, fmt.Errorf("wb api error: %s", resp.ErrorText)
	}
	return resp.Data.Feedbacks, nil
}

// AnswerFeedback posts a reply to a feedback ID.
func (c *Client) AnswerFeedback(ctx context.Context, id, text string) error {
	body := answerRequest{ID: id, Text: text}
	var generic genericResponse
	if err := c.post(ctx, "/api/v1/feedbacks/answer", body, &generic); err != nil {
		return err
	}
	if generic.Error {
		return fmt.Errorf("wb api error: %s", generic.ErrorText)
	}
	return nil
}

// --- internal helpers ---

func (c *Client) get(ctx context.Context, endpoint string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	c.addAuthHeader(req)
	return c.do(req, out)
}

func (c *Client) post(ctx context.Context, path string, payload any, out interface{}) error {
	reqURL := c.resolve(path)
	buf := &bytes.Buffer{}
	if err := json.NewEncoder(buf).Encode(payload); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.addAuthHeader(req)
	return c.do(req, out)
}

func (c *Client) do(req *http.Request, out interface{}) error {
	if err := c.wait(req.Context()); err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("wb api http %d: %s", resp.StatusCode, string(b))
	}

	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) addAuthHeader(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
}

func (c *Client) resolve(p string) string {
	u := *c.baseURL // copy
	u.Path = path.Join(u.Path, p)
	return u.String()
}

func (c *Client) wait(ctx context.Context) error {
	if c.limiter == nil || c.limiter.Limit() == rate.Inf {
		return nil
	}
	return c.limiter.Wait(ctx)
}
