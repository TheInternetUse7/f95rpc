package f95api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"f95rpc/internal/version"
)

func TestUserAgentUsesBuildVersion(t *testing.T) {
	for _, test := range []struct {
		version string
		want    string
	}{
		{"dev", "f95rpc/dev"},
		{"v1.2.3-rc.1", "f95rpc/1.2.3-rc.1"},
	} {
		t.Run(test.version, func(t *testing.T) {
			oldVersion := version.Version
			version.Version = test.version
			t.Cleanup(func() { version.Version = oldVersion })
			headers := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				headers <- r.Header.Get("User-Agent")
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"query":"test","hits":[]}`)
			}))
			defer server.Close()
			client := NewClient("")
			client.SetBaseURL(server.URL)
			if _, err := client.Search("test", 1); err != nil {
				t.Fatal(err)
			}
			if got := <-headers; got != test.want {
				t.Fatalf("User-Agent = %q, want %q", got, test.want)
			}
		})
	}
}

func TestGetBatchReadsCurrentItemsResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/threads/batch" || r.URL.Query().Get("ids") != "82285,29666" {
			t.Errorf("unexpected batch request: %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"items":[
			{"thread_id":82285,"url":"https://f95zone.to/threads/82285","title":"Eternum","creator":"Caribdis","developer":"Caribdis","version":"0.8.5","category":"games","prefixes":["Ren'Py","Ongoing"],"cover":"https://example.com/cover.jpg","banner":null,"previews":[],"downloads":[],"rating":4.9,"review_count":12,"thread_state":"active"},
			{"thread_id":29666,"url":"https://f95zone.to/threads/29666","title":"Another Game","creator":"Developer","version":"1.0","category":"games","prefixes":[],"cover":null,"banner":null,"rating":0,"thread_state":"active"}
		],"removed":[]}`)
	}))
	defer server.Close()
	client := NewClient("test-key")
	client.SetBaseURL(server.URL)
	response, err := client.GetBatch([]int64{82285, 29666})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 2 || response.Results["82285"].Title != "Eternum" {
		t.Fatalf("batch items were silently discarded: %+v", response)
	}
}

func TestGetBatchRejectsMissingItems(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{"items":null}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, body)
			}))
			defer server.Close()
			client := NewClient("test-key")
			client.SetBaseURL(server.URL)
			if _, err := client.GetBatch([]int64{82285}); err == nil {
				t.Fatal("missing items were silently treated as a successful empty batch")
			}
		})
	}
}

func TestGetHandiworkRejectsInvalidThreadResponse(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{"id":29666,"name":"Wrong Game"}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, body)
			}))
			defer server.Close()
			client := NewClient("test-key")
			client.SetBaseURL(server.URL)
			if _, err := client.GetHandiwork(82285); err == nil {
				t.Fatal("invalid metadata response must not be treated as a successful fetch")
			}
		})
	}
}

func TestClientRateLimitWaitIsCancelable(t *testing.T) {
	client := NewClient("test-key")
	client.minDelay = 2 * time.Second
	client.lastCall = time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:1", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := client.do(req)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("canceled API request remained blocked in the rate limiter")
		<-done
	}
}

func TestClientRateLimitWaitCancelsAfterWaitingStarts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := NewClient("test-key")
		client.minDelay = time.Hour
		client.lastCall = time.Now()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:1", nil)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			_, err := client.do(req)
			done <- err
		}()
		synctest.Wait() // Request is durably blocked on the rate-limit timer, not real network I/O.
		cancel()
		synctest.Wait()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("expected cancellation during rate-limit wait, got %v", err)
			}
		default:
			t.Fatal("shutdown cancellation did not interrupt the rate-limit timer")
		}
	})
}

func TestGetHandiworkContextCancelsActiveRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
			fmt.Fprint(w, `{"id":82285,"name":"Eternum"}`)
		}
	}))
	defer server.Close()
	defer close(release)
	client := NewClient("test-key")
	client.SetBaseURL(server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := client.GetHandiworkContext(ctx, 82285)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("metadata request never started")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected canceled metadata request, got %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("metadata fetch ignored shutdown cancellation")
	}
}

func TestAPIHTTPErrorExposesStatusAndRetryAfter(t *testing.T) {
	for _, test := range []struct {
		name       string
		status     int
		retryAfter string
		retryDate  bool
		wantDelay  time.Duration
	}{
		{"rate limited", http.StatusTooManyRequests, "120", false, 2 * time.Minute},
		{"retry date", http.StatusTooManyRequests, "", true, 90 * time.Second},
		{"unauthorized", http.StatusUnauthorized, "", false, 0},
		{"forbidden", http.StatusForbidden, "", false, 0},
		{"server failure", http.StatusServiceUnavailable, "invalid", false, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", test.retryAfter)
				if test.retryDate {
					w.Header().Set("Retry-After", time.Now().Add(test.wantDelay).UTC().Format(http.TimeFormat))
				}
				w.WriteHeader(test.status)
				fmt.Fprint(w, `{"detail":"request failed"}`)
			}))
			defer server.Close()
			client := NewClient("test-key")
			client.SetBaseURL(server.URL)
			_, err := client.GetHandiwork(82285)
			var httpErr *HTTPError
			if !errors.As(err, &httpErr) || httpErr.StatusCode != test.status {
				t.Fatalf("expected HTTP status %d on API error, got %v", test.status, err)
			}
			if difference := test.wantDelay - httpErr.RetryAfter; difference < 0 || difference > 2*time.Second {
				t.Fatalf("RetryAfter = %s, want approximately %s", httpErr.RetryAfter, test.wantDelay)
			}
		})
	}
}
