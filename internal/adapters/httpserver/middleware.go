package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/time/rate"
)

// loggingMiddleware records method, path, status and latency for every request.
func loggingMiddleware(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()

			next.ServeHTTP(ww, r)

			log.InfoContext(r.Context(), "request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", ww.Status()),
				slog.Duration("latency", time.Since(start)),
				slog.String("request_id", middleware.GetReqID(r.Context())),
				slog.String("cache", ww.Header().Get(headerCacheStatus)),
			)
		})
	}
}

// TimeoutMiddleware cancels the request context after d and returns 504 if the
// handler has not written a response. Unlike http.Server.WriteTimeout this
// propagates context cancellation into handler code and adapter calls.
func TimeoutMiddleware(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()

			tw := &timeoutWriter{ResponseWriter: w}
			done := make(chan struct{})

			go func() {
				next.ServeHTTP(tw, r.WithContext(ctx))
				close(done)
			}()

			select {
			case <-done:
			case <-ctx.Done():
				tw.mu.Lock()
				if !tw.written {
					tw.ResponseWriter.Header().Set("Content-Type", "application/json")
					tw.ResponseWriter.WriteHeader(http.StatusGatewayTimeout)
					_, _ = tw.ResponseWriter.Write([]byte(`{"error":"upstream timeout"}`))
					tw.written = true
				}
				tw.mu.Unlock()
				<-done
			}
		})
	}
}

type timeoutWriter struct {
	http.ResponseWriter
	mu      sync.Mutex
	written bool
}

func (tw *timeoutWriter) WriteHeader(code int) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if !tw.written {
		tw.written = true
		tw.ResponseWriter.WriteHeader(code)
	}
}

func (tw *timeoutWriter) Write(b []byte) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.written {
		return 0, nil
	}
	tw.written = true
	return tw.ResponseWriter.Write(b)
}

// RateLimiterMiddleware implements per-IP token bucket rate limiting backed by
// an in-memory map with periodic eviction of idle entries.
// rps is the sustained throughput; burst allows temporary spikes.
func RateLimiterMiddleware(rps float64, burst int) func(http.Handler) http.Handler {
	type entry struct {
		limiter  *rate.Limiter
		lastSeen time.Time
	}

	var (
		mu      sync.Mutex
		clients = make(map[string]*entry)
	)

	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			mu.Lock()
			for ip, e := range clients {
				if time.Since(e.lastSeen) > 10*time.Minute {
					delete(clients, ip)
				}
			}
			mu.Unlock()
		}
	}()

	getLimiter := func(ip string) *rate.Limiter {
		mu.Lock()
		defer mu.Unlock()
		e, ok := clients[ip]
		if !ok {
			e = &entry{limiter: rate.NewLimiter(rate.Limit(rps), burst)}
			clients[ip] = e
		}
		e.lastSeen = time.Now()
		return e.limiter
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := r.RemoteAddr
			if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
				ip = realIP
			}

			if !getLimiter(ip).Allow() {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":"rate limit exceeded"}`))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
