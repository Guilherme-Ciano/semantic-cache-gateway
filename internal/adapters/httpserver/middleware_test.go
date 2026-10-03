package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Guilherme-Ciano/semantic-cache-gateway/internal/adapters/httpserver"
)

func TestRateLimiterMiddleware(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		rps        float64
		burst      int
		requests   int
		wantStatus []int // expected status per sequential request
	}{
		{
			name:       "first request within burst is allowed",
			rps:        1,
			burst:      2,
			requests:   1,
			wantStatus: []int{http.StatusOK},
		},
		{
			name:       "requests within burst all allowed",
			rps:        10,
			burst:      3,
			requests:   3,
			wantStatus: []int{http.StatusOK, http.StatusOK, http.StatusOK},
		},
		{
			name:       "request exceeding burst is rejected with 429",
			rps:        0.001, // negligible refill
			burst:      1,
			requests:   2,
			wantStatus: []int{http.StatusOK, http.StatusTooManyRequests},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := httpserver.RateLimiterMiddleware(tc.rps, tc.burst)(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				}),
			)

			for i, want := range tc.wantStatus {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.RemoteAddr = "192.0.2.1:1234"

				handler.ServeHTTP(rec, req)

				if rec.Code != want {
					t.Errorf("request %d: status = %d, want %d", i+1, rec.Code, want)
				}
			}
		})
	}
}

func TestTimeoutMiddleware(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		timeout    time.Duration
		handlerFn  func(w http.ResponseWriter, r *http.Request)
		wantStatus int
	}{
		{
			name:    "fast handler returns before timeout",
			timeout: 100 * time.Millisecond,
			handlerFn: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			},
			wantStatus: http.StatusOK,
		},
		{
			name:    "slow handler exceeds timeout and gets 504",
			timeout: 10 * time.Millisecond,
			handlerFn: func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-time.After(500 * time.Millisecond):
					w.WriteHeader(http.StatusOK)
				case <-r.Context().Done():
				}
			},
			wantStatus: http.StatusGatewayTimeout,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := httpserver.TimeoutMiddleware(tc.timeout)(
				http.HandlerFunc(tc.handlerFn),
			)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)

			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
		})
	}
}
