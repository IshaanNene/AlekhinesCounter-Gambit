package auth

import (
	"net"
	"net/http"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/redisx"
)

// RateLimitMiddleware rejects callers who exceed their token bucket.
//
// Keyed by user id when signed in, falling back to client IP. Per-user is the
// meaningful unit — several players legitimately share an office IP, and one
// abusive account should not throttle their colleagues — but an anonymous
// caller has no id, so IP is the only handle we have.
//
// Must be installed *inside* the auth middleware so the identity is available.
func RateLimitMiddleware(limiter *redisx.Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := "ip:" + clientIP(r)
			if id, err := FromContext(r.Context()); err == nil {
				key = "user:" + id.UserID
			}

			allowed, remaining := limiter.Allow(r.Context(), key)
			w.Header().Set("X-RateLimit-Remaining", itoa(remaining))
			if !allowed {
				w.Header().Set("Retry-After", "1")
				http.Error(w, `{"errors":[{"message":"rate limit exceeded — slow down"}]}`,
					http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIP prefers X-Forwarded-For's first entry, since the gateway sits behind
// NGINX and RemoteAddr would otherwise be the proxy for every caller.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// The left-most entry is the original client; the rest are proxies.
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return trimSpace(xff[:i])
			}
		}
		return trimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
