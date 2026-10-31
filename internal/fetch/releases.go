package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/morehao/trendradar/internal/ipc"
)

// Release is the subset of a GitHub release we use.
type Release struct {
	ID          int64      `json:"id"`
	TagName     string     `json:"tag_name"`
	Name        string     `json:"name"`
	Body        string     `json:"body"`
	Draft       bool       `json:"draft"`
	Prerelease  bool       `json:"prerelease"`
	HTMLURL     string     `json:"html_url"`
	PublishedAt *time.Time `json:"published_at"`
}

// Tag is the subset of a GitHub tag we use.
type Tag struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// ReleasePage is one page of releases. HasNext is derived from a full page,
// which is accurate for GitHub's fixed per_page paging.
type ReleasePage struct {
	Releases []Release
	HasNext  bool
	NextPage int
}

// Commit is the subset of a GitHub commit we use (for tag-only repos that have
// no release notes).
type Commit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message string `json:"message"`
		Author  struct {
			Date *time.Time `json:"date"`
		} `json:"author"`
	} `json:"commit"`
}

// TagPage is one page of tags.
type TagPage struct {
	Tags     []Tag
	HasNext  bool
	NextPage int
}

// ListReleasesPage fetches one page of releases (newest first).
func (c *Client) ListReleasesPage(ctx context.Context, owner, repo string, page, pageSize int) (ReleasePage, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 30
	}
	path := fmt.Sprintf("/repos/%s/%s/releases?per_page=%d&page=%d",
		url.PathEscape(owner), url.PathEscape(repo), pageSize, page)
	body, err := c.getAPI(ctx, path)
	if err != nil {
		return ReleasePage{}, err
	}
	var rels []Release
	if err := json.Unmarshal(body, &rels); err != nil {
		return ReleasePage{}, &Error{Code: ipc.CodeFetchFailed, Message: "decode releases", Err: err}
	}
	return ReleasePage{Releases: rels, HasNext: len(rels) == pageSize, NextPage: page + 1}, nil
}

// ListTagsPage fetches one page of tags (newest first by commit order).
func (c *Client) ListTagsPage(ctx context.Context, owner, repo string, page, pageSize int) (TagPage, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 30
	}
	path := fmt.Sprintf("/repos/%s/%s/tags?per_page=%d&page=%d",
		url.PathEscape(owner), url.PathEscape(repo), pageSize, page)
	body, err := c.getAPI(ctx, path)
	if err != nil {
		return TagPage{}, err
	}
	var tags []Tag
	if err := json.Unmarshal(body, &tags); err != nil {
		return TagPage{}, &Error{Code: ipc.CodeFetchFailed, Message: "decode tags", Err: err}
	}
	return TagPage{Tags: tags, HasNext: len(tags) == pageSize, NextPage: page + 1}, nil
}

// GetCommit fetches one commit (used to give tag-only releases a summary basis).
func (c *Client) GetCommit(ctx context.Context, owner, repo, sha string) (Commit, error) {
	if sha == "" {
		return Commit{}, &Error{Code: ipc.CodeFetchFailed, Message: "empty commit sha"}
	}
	path := fmt.Sprintf("/repos/%s/%s/commits/%s", url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(sha))
	body, err := c.getAPI(ctx, path)
	if err != nil {
		return Commit{}, err
	}
	var cm Commit
	if err := json.Unmarshal(body, &cm); err != nil {
		return Commit{}, &Error{Code: ipc.CodeFetchFailed, Message: "decode commit", Err: err}
	}
	return cm, nil
}
