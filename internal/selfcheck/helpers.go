package selfcheck

import (
	"net/http"
	"time"
)

// httpClient returns a small-timeout client for the in-process httptest server.
func httpClient(_ string) *http.Client {
	return &http.Client{Timeout: 10 * time.Second}
}
