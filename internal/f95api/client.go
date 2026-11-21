package f95api

import (
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
	Results map[string]ThreadSummary `json:"results"`
}

func (c *Client) do(req *http.Request) ([]byte, error) {
	c.mu.Lock()
	now := time.Now()
	elapsed := now.Sub(c.lastCall)
	if elapsed < c.minDelay {
		time.Sleep(c.minDelay - elapsed)
	}
	c.lastCall = time.Now()
	c.mu.Unlock()

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

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("rate limited by f95api (HTTP 429)")
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("unauthorized: invalid or missing f95api key (HTTP 401)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("f95api returned HTTP %d: %s", resp.StatusCode, string(body))
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
	endpoint := fmt.Sprintf("%s/v1/f95/handiwork?thread_id=%d", c.baseURL, threadID)
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
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

	var res ThreadBatchResponse
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("decode batch response: %w", err)
	}
	return &res, nil
}
