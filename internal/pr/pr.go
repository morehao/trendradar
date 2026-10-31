// Package pr is a minimal GitHub pull-request client (REST). The token is
// injected at call time and never written to disk.
package pr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/morehao/trendradar/internal/ipc"
)

// Client talks to the GitHub REST API for a single repository.
type Client struct {
	Owner, Repo string
	Token       string
	BaseURL     string
	HTTP        *http.Client
}

// New builds a client.
func New(owner, repo, token string) *Client {
	return &Client{
		Owner: owner, Repo: repo, Token: token,
		BaseURL: "https://api.github.com",
		HTTP:    &http.Client{Timeout: 20 * time.Second},
	}
}

// Pull is the subset of the PR object we use. MergedAt is nil until the PR is
// merged; "state" alone cannot distinguish merged from closed-unmerged.
type Pull struct {
	Number   int        `json:"number"`
	HTMLURL  string     `json:"html_url"`
	State    string     `json:"state"`
	MergedAt *time.Time `json:"merged_at"`
}

// Merged reports whether the PR was merged (as opposed to closed unmerged).
func (p *Pull) Merged() bool { return p != nil && p.MergedAt != nil }

// ClosedUnmerged reports whether a human closed the PR without merging it.
// Automated runs treat this as "this date was rejected" and do not reopen it;
// a manual run may.
func (p *Pull) ClosedUnmerged() bool { return p != nil && p.State == "closed" && p.MergedAt == nil }

var remoteRe = regexp.MustCompile(`github\.com[:/]+([^/]+)/([^/]+?)(?:\.git)?$`)

// ParseRemote extracts owner/repo from a git remote URL.
func ParseRemote(remote string) (owner, repo string, err error) {
	remote = strings.TrimSpace(remote)
	m := remoteRe.FindStringSubmatch(remote)
	if len(m) != 3 {
		return "", "", fmt.Errorf("pr: cannot parse github remote %q", remote)
	}
	return m[1], strings.TrimSuffix(m[2], ".git"), nil
}

func (c *Client) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, &ipc.ErrorInfo{Code: ipc.CodePRFailed, Message: err.Error(), Retriable: ipc.Retriable(ipc.CodePRFailed)}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("pr: %s %s -> %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

// FindOpen returns an open PR for head->base, or nil.
func (c *Client) FindOpen(ctx context.Context, head, base string) (*Pull, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls?state=open&head=%s:%s&base=%s", c.Owner, c.Repo, c.Owner, head, base)
	data, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var pulls []Pull
	if err := json.Unmarshal(data, &pulls); err != nil {
		return nil, err
	}
	if len(pulls) == 0 {
		return nil, nil
	}
	return &pulls[0], nil
}

// FindForBranch returns the most recently created PR (in ANY state) for
// head->base, or nil. The lifecycle of the digest branch for a date is decided
// from this: a merged PR means the date is done; a closed-unmerged PR means the
// branch may be rebuilt and a fresh PR opened.
func (c *Client) FindForBranch(ctx context.Context, head, base string) (*Pull, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls?state=all&head=%s:%s&base=%s&sort=created&direction=desc&per_page=1",
		c.Owner, c.Repo, c.Owner, head, base)
	data, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var pulls []Pull
	if err := json.Unmarshal(data, &pulls); err != nil {
		return nil, err
	}
	if len(pulls) == 0 {
		return nil, nil
	}
	return &pulls[0], nil
}

// Create opens a PR.
func (c *Client) Create(ctx context.Context, title, head, base, bodyText string) (*Pull, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls", c.Owner, c.Repo)
	data, err := c.do(ctx, http.MethodPost, path, map[string]any{"title": title, "head": head, "base": base, "body": bodyText})
	if err != nil {
		return nil, err
	}
	var p Pull
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// Update edits an existing PR's title/body.
func (c *Client) Update(ctx context.Context, number int, title, bodyText string) (*Pull, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", c.Owner, c.Repo, number)
	data, err := c.do(ctx, http.MethodPatch, path, map[string]any{"title": title, "body": bodyText})
	if err != nil {
		return nil, err
	}
	var p Pull
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// Merge merges a PR (squash). 405/409 map to NOT_MERGEABLE.
func (c *Client) Merge(ctx context.Context, number int) error {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/merge", c.Owner, c.Repo, number)
	_, err := c.do(ctx, http.MethodPut, path, map[string]any{"merge_method": "squash"})
	if err != nil {
		if strings.Contains(err.Error(), "-> 405") || strings.Contains(err.Error(), "-> 409") {
			return &ipc.ErrorInfo{Code: ipc.CodeNotMergeable, Message: err.Error(), Retriable: false}
		}
		return &ipc.ErrorInfo{Code: ipc.CodePRFailed, Message: err.Error(), Retriable: ipc.Retriable(ipc.CodePRFailed)}
	}
	return nil
}
