// Package fetch talks to GitHub: it scrapes trending HTML and, as a fallback,
// queries the search API. All HTTP goes through golib/protocol/ghttp so retry,
// connection pooling and response-size limits are shared and consistent.
package fetch

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/morehao/golib/protocol"
	"github.com/morehao/golib/protocol/ghttp"

	"github.com/morehao/trendradar/internal/ipc"
)

const (
	maxBodyBytes   = 2 << 20 // 2 MiB
	defaultHTMLURL = "https://github.com"
	defaultAPIURL  = "https://api.github.com"
)

// Error carries a stable IPC code so callers can branch on failure type.
type Error struct {
	Code    ipc.Code
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

// Options configures a Client.
type Options struct {
	BaseURL       string        // HTML base, default https://github.com
	APIBaseURL    string        // search API base, default https://api.github.com
	UserAgent     string        // default trendradar/<version>
	Timeout       time.Duration // default 10s
	MaxRetry      int           // total attempts incl. first, default 2
	RetryInterval time.Duration // default 1s
	// Token is an optional GitHub token. It only raises the API rate limit for
	// public repos today; private repos are out of scope.
	Token string
}

// Client wraps the HTML and API ghttp clients.
type Client struct {
	html      *ghttp.Client
	api       *ghttp.Client
	userAgent string
	token     string
}

// NewClient builds a Client with sensible defaults applied.
func NewClient(opts Options) *Client {
	if opts.BaseURL == "" {
		opts.BaseURL = defaultHTMLURL
	}
	if opts.APIBaseURL == "" {
		opts.APIBaseURL = defaultAPIURL
	}
	if opts.UserAgent == "" {
		opts.UserAgent = "trendradar/" + ipc.Version
	}
	if opts.Timeout == 0 {
		opts.Timeout = 10 * time.Second
	}
	if opts.MaxRetry == 0 {
		opts.MaxRetry = 2
	}
	if opts.RetryInterval == 0 {
		opts.RetryInterval = time.Second
	}

	newHTTP := func(host string) *ghttp.Client {
		return ghttp.NewClient(&protocol.HttpClientConfig{
			Host:             host,
			Timeout:          opts.Timeout,
			MaxRetry:         opts.MaxRetry,
			RetryOnStatus:    []int{429, 500, 502, 503, 504},
			RetryInterval:    opts.RetryInterval,
			MaxResponseBytes: maxBodyBytes,
		})
	}

	return &Client{
		html:      newHTTP(opts.BaseURL),
		api:       newHTTP(opts.APIBaseURL),
		userAgent: opts.UserAgent,
		token:     opts.Token,
	}
}

func (c *Client) headers() map[string]string {
	h := map[string]string{
		"User-Agent":      c.userAgent,
		"Accept":          "text/html,application/vnd.github+json",
		"Accept-Language": "en-US,en;q=0.9",
	}
	if c.token != "" {
		h["Authorization"] = "Bearer " + c.token
	}
	return h
}

func (c *Client) getHTML(ctx context.Context, path string) ([]byte, error) {
	return c.get(ctx, c.html, path)
}

func (c *Client) getAPI(ctx context.Context, path string) ([]byte, error) {
	return c.get(ctx, c.api, path)
}

func (c *Client) get(ctx context.Context, hc *ghttp.Client, path string) ([]byte, error) {
	res, err := hc.Get(ctx, path, ghttp.RequestOption{Headers: c.headers()})
	if err != nil {
		return nil, &Error{Code: ipc.CodeFetchFailed, Message: "request failed", Err: err}
	}
	if !res.IsSuccess() {
		return nil, statusError(res.HttpCode)
	}
	return res.Bytes(), nil
}

func statusError(code int) *Error {
	if code == http.StatusTooManyRequests {
		return &Error{Code: ipc.CodeFetchRateLimited, Message: fmt.Sprintf("status %d", code)}
	}
	return &Error{Code: ipc.CodeFetchFailed, Message: fmt.Sprintf("unexpected status %d", code)}
}
