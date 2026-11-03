package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/morehao/golib/protocol/ghttp"

	"github.com/morehao/trendradar/internal/ipc"
)

// graphqlBatchSize is how many repository aliases go into one GraphQL request.
// GitHub's GraphQL API costs 1 point per connection/page but the node lookups
// here are cheap; 100 aliases per request keeps 300 repos at ~3 requests/day,
// well under the 5000-points/hour ceiling (plan D2 / risk table).
const graphqlBatchSize = 100

// StarCount is one repository's current stargazers count, keyed by full name.
type StarCount struct {
	FullName string
	Owner    string
	Name     string
	Stars    int
	Forks    int
}

// graphqlRequest / response envelopes for the batched repository query.
type graphqlStarsRequest struct {
	Query string `json:"query"`
}

type graphqlStarsResponse struct {
	Data   map[string]graphqlRepoNode `json:"data"`
	Errors []graphqlError             `json:"errors"`
}

type graphqlRepoNode struct {
	StargazerCount int `json:"stargazerCount"`
	ForkCount      int `json:"forkCount"`
}

type graphqlError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// splitFullName splits "owner/name" into its two halves. The second return is
// false when the input is not of that exact shape, so callers can skip bad
// candidates instead of querying GitHub for garbage.
func splitFullName(fullName string) (owner, name string, ok bool) {
	parts := strings.Split(strings.TrimSpace(fullName), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// aliasFor builds a stable, GraphQL-safe alias from a batch index. Aliases must
// be valid GraphQL names, so we index rather than embed the repo name (which may
// contain '-' or '.').
func aliasFor(i int) string { return fmt.Sprintf("r%d", i) }

// buildStarsQuery assembles a batched `repository(owner:, name:)` query with one
// aliased field per repo. Malformed names are skipped (and reported back) so one
// bad candidate never fails the whole batch.
func buildStarsQuery(repos []StarCount) (query string, used []StarCount) {
	var b strings.Builder
	b.WriteString("query {\n")
	for _, r := range repos {
		owner, name, ok := splitFullName(r.FullName)
		if !ok {
			continue
		}
		r.Owner, r.Name = owner, name
		fmt.Fprintf(&b, "  %s: repository(owner: %q, name: %q) { stargazerCount forkCount }\n", aliasFor(len(used)), owner, name)
		used = append(used, r)
	}
	b.WriteString("}")
	return b.String(), used
}

// FetchStars fetches current stargazers counts for the given repositories using
// batched GraphQL requests (graphqlBatchSize aliases per request). It returns a
// map keyed by full name. Repos GitHub reports as null (deleted/renamed) are
// simply absent from the result, not treated as errors. A batch whose request
// itself fails is reported in the returned error but does not discard the other
// batches' results — the caller decides whether a partial map is enough.
func (c *Client) FetchStars(ctx context.Context, repos []string) (map[string]StarCount, error) {
	nodes := make([]StarCount, 0, len(repos))
	for _, fn := range repos {
		if _, _, ok := splitFullName(fn); !ok {
			continue
		}
		nodes = append(nodes, StarCount{FullName: strings.TrimSpace(fn)})
	}

	out := make(map[string]StarCount, len(nodes))
	var firstErr error
	for start := 0; start < len(nodes); start += graphqlBatchSize {
		end := start + graphqlBatchSize
		if end > len(nodes) {
			end = len(nodes)
		}
		got, err := c.fetchStarsBatch(ctx, nodes[start:end])
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for k, v := range got {
			out[k] = v
		}
	}
	return out, firstErr
}

// fetchStarsBatch issues one GraphQL request for up to graphqlBatchSize repos.
func (c *Client) fetchStarsBatch(ctx context.Context, batch []StarCount) (map[string]StarCount, error) {
	query, used := buildStarsQuery(batch)
	if len(used) == 0 {
		return map[string]StarCount{}, nil
	}
	payload, err := json.Marshal(graphqlStarsRequest{Query: query})
	if err != nil {
		return nil, &Error{Code: ipc.CodeFetchFailed, Message: "encode graphql request", Err: err}
	}

	body, err := c.postAPI(ctx, "/graphql", payload)
	if err != nil {
		return nil, err
	}
	var resp graphqlStarsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, &Error{Code: ipc.CodeFetchFailed, Message: "decode graphql response", Err: err}
	}
	if len(resp.Errors) > 0 && len(resp.Data) == 0 {
		return nil, &Error{Code: ipc.CodeFetchFailed, Message: "graphql errors: " + resp.Errors[0].Message}
	}

	out := make(map[string]StarCount, len(used))
	for i, r := range used {
		node, ok := resp.Data[aliasFor(i)]
		if !ok {
			continue // repository null (deleted/renamed): skip, don't error
		}
		out[r.FullName] = StarCount{
			FullName: r.FullName,
			Owner:    r.Owner,
			Name:     r.Name,
			Stars:    node.StargazerCount,
			Forks:    node.ForkCount,
		}
	}
	return out, nil
}

// StarHistoryPoint is one weekly bucket from the stargazers/history endpoint. Days
// is the number of days the bucket covers (normally 7, less for the current
// partial week), so a backfill can spread the count honestly instead of assuming
// a full week.
type StarHistoryPoint struct {
	WeekStart string `json:"weekStart"` // "YYYY-MM-DD", UTC Monday
	Stars     int    `json:"stars"`
	Days      int    `json:"days"`
}

type starHistoryResponse struct {
	Weeks []StarHistoryPoint `json:"weeks"`
	// HasNext / Cursor support the paginated history endpoint; they mirror the
	// shape GitHub-style paginated APIs use so page walking is straightforward.
	HasNext bool   `json:"hasNext"`
	Cursor  string `json:"cursor"`
}

// starHistoryPageLimit caps how many pages we walk during a backfill. It bounds
// a first-time import to something that finishes quickly; a longer history is
// simply not backfilled (later runs keep appending), which is acceptable since
// backfill is an accelerator, not a correctness requirement (plan risk table).
const starHistoryPageLimit = 100

// BackfillHistory fetches the weekly stargazer history for one repo, following
// pagination up to starHistoryPageLimit pages. It is used to seed the observation
// store when a repo is first seen. A failure here never blocks a run: callers log
// the returned error as a warning and start accumulating from today instead.
func (c *Client) BackfillHistory(ctx context.Context, fullName string) ([]StarHistoryPoint, error) {
	owner, name, ok := splitFullName(fullName)
	if !ok {
		return nil, &Error{Code: ipc.CodeFetchFailed, Message: "invalid repo full name: " + fullName}
	}
	base := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/stargazers/history"

	var (
		out    []StarHistoryPoint
		cursor string
	)
	for page := 0; page < starHistoryPageLimit; page++ {
		path := base
		if cursor != "" {
			path += "?cursor=" + url.QueryEscape(cursor)
		}
		body, err := c.getAPI(ctx, path)
		if err != nil {
			return out, err
		}
		var resp starHistoryResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return out, &Error{Code: ipc.CodeFetchFailed, Message: "decode star history", Err: err}
		}
		out = append(out, resp.Weeks...)
		if !resp.HasNext || resp.Cursor == "" {
			break
		}
		cursor = resp.Cursor
	}
	return out, nil
}

// postAPI issues an authenticated POST with a JSON body against the API host.
// It mirrors getAPI's error handling (status → Error with a stable code) but is
// separate because the shared Client.get is GET-only and GraphQL requires POST.
func (c *Client) postAPI(ctx context.Context, path string, body []byte) ([]byte, error) {
	headers := c.headers()
	headers["Content-Type"] = "application/json"
	opt := ghttp.RequestOption{RequestBody: body, Headers: headers, ContentType: "application/json"}
	res, err := c.api.Post(ctx, path, opt)
	if err != nil {
		return nil, &Error{Code: ipc.CodeFetchFailed, Message: "request failed", Err: err}
	}
	if !res.IsSuccess() {
		return nil, statusError(res.HttpCode)
	}
	return res.Bytes(), nil
}
