package f95api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

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
