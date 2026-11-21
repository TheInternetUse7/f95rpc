package f95api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"f95rpc/internal/version"
)

const (
	DefaultBaseURL = "https://f95api.xyz"
	DefaultLimit   = 5
)

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	mu         sync.Mutex
	lastCall   time.Time
	minDelay   time.Duration
}

type HTTPError struct {
	StatusCode int
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	switch e.StatusCode {
	case http.StatusTooManyRequests:
		return "rate limited by f95api (HTTP 429)"
	case http.StatusUnauthorized:
		return "unauthorized: invalid or missing f95api key (HTTP 401)"
	case http.StatusForbidden:
		return "forbidden: f95api key lacks permission (HTTP 403)"
	}
	return fmt.Sprintf("f95api returned HTTP %d", e.StatusCode)
}

func NewClient(apiKey string) *Client {
	return &Client{
		baseURL: DefaultBaseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		minDelay: 500 * time.Millisecond, // Rate-limit safety: max ~120 req/min
	}
}

func (c *Client) SetBaseURL(url string) {
	c.baseURL = strings.TrimRight(url, "/")
}

type SearchHit struct {
	ThreadID int64    `json:"thread_id"`
	Title    string   `json:"title"`
	Creator  string   `json:"creator"`
	URL      string   `json:"url"`
	Category string   `json:"category"`
	Score    *float64 `json:"score"`
	Cover    *string  `json:"cover"`
	Banner   *string  `json:"banner"`
}

type SearchResponse struct {
	Query string      `json:"query"`
	Hits  []SearchHit `json:"hits"`
}

type Rating struct {
	Average float64 `json:"average"`
	Count   int     `json:"count"`
}

type Author struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type Handiwork struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	Overview string   `json:"overview"`
	Authors  []Author `json:"authors"`
	Category string   `json:"category"`
	Cover    *string  `json:"cover"`
	Banner   *string  `json:"banner"`
	Rating   Rating   `json:"rating"`
	Tags     []string `json:"tags"`
	URL      string   `json:"url"`
	Engine   string   `json:"engine"`
	Status   string   `json:"status"`
	Version  string   `json:"version"`
}

type ThreadSummary struct {
	ThreadID int64    `json:"thread_id"`
	URL      string   `json:"url"`
	Title    string   `json:"title"`
	Creator  string   `json:"creator"`
	Version  string   `json:"version"`
	Category string   `json:"category"`
	Cover    *string  `json:"cover"`
	Banner   *string  `json:"banner"`
	Tags     []string `json:"tag_names"`
}

type ThreadBatchResponse struct {
	// Normalize the API's items array to a lookup keyed by thread ID.
	Results map[string]ThreadSummary
}

func (c *Client) do(req *http.Request) ([]byte, error) {
	for {
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		c.mu.Lock()
		wait := time.Until(c.lastCall.Add(c.minDelay))
		if wait <= 0 {
			c.lastCall = time.Now()
			c.mu.Unlock()
			break
		}
		c.mu.Unlock()

		timer := time.NewTimer(wait)
		select {
		case <-req.Context().Done():
			timer.Stop()
			return nil, req.Context().Err()
		case <-timer.C:
		}
	}

	req.Header.Set("User-Agent", "f95rpc/"+strings.TrimPrefix(version.Version, "v"))
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		httpErr := &HTTPError{StatusCode: resp.StatusCode}
		retryAfter := strings.TrimSpace(resp.Header.Get("Retry-After"))
		if seconds, err := strconv.ParseUint(retryAfter, 10, 32); err == nil {
			httpErr.RetryAfter = time.Duration(seconds) * time.Second
		} else if deadline, err := http.ParseTime(retryAfter); err == nil {
			if delay := time.Until(deadline); delay > 0 {
				httpErr.RetryAfter = delay
			}
		}
		return nil, httpErr
	}

	return body, nil
}

func (c *Client) Search(query string, limit int) (*SearchResponse, error) {
	if limit <= 0 {
		limit = DefaultLimit
	}
	endpoint := fmt.Sprintf("%s/v1/search?q=%s&limit=%d", c.baseURL, url.QueryEscape(query), limit)
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	data, err := c.do(req)
	if err != nil {
		return nil, err
	}

	var res SearchResponse
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}
	return &res, nil
}

func (c *Client) GetHandiwork(threadID int64) (*Handiwork, error) {
	return c.GetHandiworkContext(context.Background(), threadID)
}

func (c *Client) GetHandiworkContext(ctx context.Context, threadID int64) (*Handiwork, error) {
	if threadID <= 0 {
		return nil, fmt.Errorf("invalid F95 thread ID: %d", threadID)
	}
	endpoint := fmt.Sprintf("%s/v1/f95/handiwork?thread_id=%d", c.baseURL, threadID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	data, err := c.do(req)
	if err != nil {
		return nil, err
	}

	var res Handiwork
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("decode handiwork response: %w", err)
	}
	if res.ID != threadID {
		return nil, fmt.Errorf("handiwork response has thread ID %d, expected %d", res.ID, threadID)
	}
	return &res, nil
}

func (c *Client) GetBatch(threadIDs []int64) (*ThreadBatchResponse, error) {
	if len(threadIDs) == 0 {
		return &ThreadBatchResponse{Results: make(map[string]ThreadSummary)}, nil
	}

	idStrs := make([]string, len(threadIDs))
	for i, id := range threadIDs {
		idStrs[i] = strconv.FormatInt(id, 10)
	}

	endpoint := fmt.Sprintf("%s/v1/threads/batch?ids=%s", c.baseURL, strings.Join(idStrs, ","))
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	data, err := c.do(req)
	if err != nil {
		return nil, err
	}

	var response struct {
		Items []ThreadSummary `json:"items"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("decode batch response: %w", err)
	}
	if response.Items == nil {
		return nil, fmt.Errorf("decode batch response: missing items array")
	}
	res := &ThreadBatchResponse{Results: make(map[string]ThreadSummary, len(response.Items))}
	for _, item := range response.Items {
		res.Results[strconv.FormatInt(item.ThreadID, 10)] = item
	}
	return res, nil
}
