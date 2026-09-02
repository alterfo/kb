package web

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// clientRateLimiterSweepEvery bounds how often allow() pays for an O(n)
// sweep of every tracked client, so a busy server still amortizes it away
// while a key that stops sending requests is eventually evicted instead of
// living in the map forever.
const clientRateLimiterSweepEvery = 1024

type clientRateLimiter struct {
	mu      sync.Mutex
	limit   int
	now     func() time.Time
	clients map[string][]time.Time
	calls   int
}

func newClientRateLimiter(limit int, now func() time.Time) *clientRateLimiter {
	if limit <= 0 {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	return &clientRateLimiter{limit: limit, now: now, clients: make(map[string][]time.Time)}
}

func (l *clientRateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := l.now().Add(-time.Minute)
	times := l.clients[key]
	start := 0
	for start < len(times) && times[start].Before(cutoff) {
		start++
	}
	if start > 0 {
		times = append([]time.Time(nil), times[start:]...)
	}
	allowed := true
	if len(times) >= l.limit {
		allowed = false
	} else {
		times = append(times, l.now())
	}
	if len(times) == 0 {
		delete(l.clients, key)
	} else {
		l.clients[key] = times
	}
	l.sweepLocked(cutoff)
	return allowed
}

// sweepLocked evicts every tracked client whose whole window has expired,
// bounding l.clients to roughly the number of clients active in the last
// minute rather than every client ever seen. Called under l.mu.
func (l *clientRateLimiter) sweepLocked(cutoff time.Time) {
	l.calls++
	if l.calls%clientRateLimiterSweepEvery != 0 {
		return
	}
	for key, times := range l.clients {
		start := 0
		for start < len(times) && times[start].Before(cutoff) {
			start++
		}
		if start == len(times) {
			delete(l.clients, key)
		} else if start > 0 {
			l.clients[key] = append([]time.Time(nil), times[start:]...)
		}
	}
}

func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.deps.RequireAuth && s.limiter == nil {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		// Rate-limit before checking auth: otherwise an unauthenticated
		// caller (the actual brute-force/abuse threat) always fails the
		// auth check first and the limiter never sees the request.
		if s.limiter != nil && !s.limiter.allow(clientKey(r)) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		if s.deps.RequireAuth && !authorized(r, s.deps.AuthToken) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="kb"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func authorized(r *http.Request, token string) bool {
	if token == "" {
		return true
	}
	if candidate := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); candidate != r.Header.Get("Authorization") {
		return constantTokenEqual(candidate, token)
	}
	if candidate := r.Header.Get("X-KB-Token"); candidate != "" {
		return constantTokenEqual(candidate, token)
	}
	if cookie, err := r.Cookie("kb_token"); err == nil {
		return constantTokenEqual(cookie.Value, token)
	}
	return false
}

func constantTokenEqual(candidate, token string) bool {
	if candidate == "" || token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(token)) == 1
}

func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}
