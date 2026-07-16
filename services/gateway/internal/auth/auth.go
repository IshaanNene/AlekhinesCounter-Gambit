// Package auth issues and verifies the gateway's session tokens.
//
// The gateway — not the identity service — owns sessions. Credentials are
// checked upstream; here we mint a short-lived JWT and read it back on each
// request. That keeps session policy (lifetime, cookie flags) at the edge, and
// means resolvers never trust a caller-supplied user id.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Issuer identifies tokens minted by this service.
const issuer = "alekhine-gateway"

// CookieName is the session cookie. It is httpOnly so page scripts — including
// anything injected via XSS — cannot read the token.
const CookieName = "acg_session"

// DefaultTTL is how long a session lasts.
const DefaultTTL = 30 * 24 * time.Hour

// ErrNoIdentity means the request carried no valid session.
var ErrNoIdentity = errors.New("not signed in")

// Identity is the authenticated caller.
type Identity struct {
	UserID   string
	Username string
	IsGuest  bool
}

// Claims is our JWT payload.
type Claims struct {
	jwt.RegisteredClaims
	Username string `json:"username"`
	IsGuest  bool   `json:"guest"`
}

// Signer mints and verifies session tokens.
type Signer struct {
	secret []byte
	ttl    time.Duration
	secure bool
}

// NewSigner builds a Signer. secret must be non-empty; secure marks cookies
// Secure (HTTPS only) and should be true anywhere but local http.
func NewSigner(secret string, ttl time.Duration, secure bool) (*Signer, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("session secret must be at least 32 bytes, got %d", len(secret))
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Signer{secret: []byte(secret), ttl: ttl, secure: secure}, nil
}

// Mint returns a signed token for the identity.
func (s *Signer) Mint(id Identity) (string, time.Time, error) {
	expiresAt := time.Now().Add(s.ttl)
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   id.UserID,
			Issuer:    issuer,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
		Username: id.Username,
		IsGuest:  id.IsGuest,
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign token: %w", err)
	}
	return signed, expiresAt, nil
}

// Verify parses and validates a token.
func (s *Signer) Verify(token string) (*Identity, error) {
	parsed, err := jwt.ParseWithClaims(token, &Claims{},
		func(t *jwt.Token) (any, error) {
			// Pin the algorithm. Without this check a token could arrive signed
			// with "none", or with HS256 abusing an RSA public key as the secret.
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method %q", t.Header["alg"])
			}
			return s.secret, nil
		},
		jwt.WithIssuer(issuer),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
	)
	if err != nil {
		return nil, fmt.Errorf("invalid session: %w", err)
	}
	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid || claims.Subject == "" {
		return nil, errors.New("invalid session claims")
	}
	return &Identity{UserID: claims.Subject, Username: claims.Username, IsGuest: claims.IsGuest}, nil
}

// SetCookie writes the session cookie.
func (s *Signer) SetCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true, // unreadable from JS, so XSS cannot exfiltrate it
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode, // blocks cross-site POST/CSRF
	})
}

// ClearCookie expires the session cookie.
func (s *Signer) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ── request context ─────────────────────────────────────────────────────────

type ctxKey int

const (
	identityKey ctxKey = iota
	writerKey
)

// Middleware verifies the session cookie (or Authorization: Bearer header) and
// attaches the identity to the request context.
//
// An invalid token is not an error: the request simply proceeds anonymously and
// resolvers that require a user reject it themselves. That keeps public queries
// working for signed-out visitors.
func (s *Signer) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), writerKey, w)

		if token := tokenFrom(r); token != "" {
			if id, err := s.Verify(token); err == nil {
				ctx = context.WithValue(ctx, identityKey, id)
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// tokenFrom pulls a token from the cookie, falling back to a bearer header so
// non-browser clients (and the WebSocket handshake) can authenticate too.
func tokenFrom(r *http.Request) string {
	if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
		return c.Value
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

// WithIdentity attaches an identity to a context.
//
// Used by the WebSocket connection_init path, which authenticates outside the
// HTTP middleware: a browser sends the session cookie on a same-origin upgrade
// automatically, but any other client (a load test, a bot, a native app) has no
// way to attach one and must pass the token in the init payload instead.
func WithIdentity(ctx context.Context, id *Identity) context.Context {
	return context.WithValue(ctx, identityKey, id)
}

// VerifyInitPayload authenticates a graphql-transport-ws connection_init.
//
// It reads an "Authorization: Bearer <token>" entry from the payload. An absent
// or bad token is not an error: the connection proceeds anonymously (the cookie
// may already have identified it), and subscriptions that need a user reject it
// themselves.
func (s *Signer) VerifyInitPayload(ctx context.Context, payload map[string]any) context.Context {
	raw, _ := payload["Authorization"].(string)
	if raw == "" {
		raw, _ = payload["authorization"].(string)
	}
	token := strings.TrimPrefix(raw, "Bearer ")
	if token == "" || token == raw && !strings.HasPrefix(raw, "Bearer ") {
		return ctx
	}
	id, err := s.Verify(token)
	if err != nil {
		return ctx
	}
	return WithIdentity(ctx, id)
}

// FromContext returns the authenticated identity, or ErrNoIdentity.
func FromContext(ctx context.Context) (*Identity, error) {
	id, ok := ctx.Value(identityKey).(*Identity)
	if !ok || id == nil {
		return nil, ErrNoIdentity
	}
	return id, nil
}

// WriterFromContext returns the ResponseWriter, so a resolver can set the
// session cookie. Absent on WebSocket-borne operations, where there is no
// response to attach a cookie to.
func WriterFromContext(ctx context.Context) (http.ResponseWriter, bool) {
	w, ok := ctx.Value(writerKey).(http.ResponseWriter)
	return w, ok
}
