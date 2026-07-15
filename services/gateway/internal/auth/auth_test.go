package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test-secret-that-is-long-enough-to-pass-32"

func newSigner(t *testing.T) *Signer {
	t.Helper()
	s, err := NewSigner(testSecret, time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNewSignerRejectsWeakSecret(t *testing.T) {
	if _, err := NewSigner("short", time.Hour, false); err == nil {
		t.Error("expected a short secret to be rejected")
	}
}

func TestMintAndVerifyRoundTrip(t *testing.T) {
	s := newSigner(t)
	want := Identity{UserID: "u-1", Username: "alice", IsGuest: false}

	token, expiresAt, err := s.Mint(want)
	if err != nil {
		t.Fatal(err)
	}
	if !expiresAt.After(time.Now()) {
		t.Errorf("expiry %v is not in the future", expiresAt)
	}

	got, err := s.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if *got != want {
		t.Errorf("round trip: got %+v, want %+v", *got, want)
	}
}

func TestVerifyRejectsTamperedToken(t *testing.T) {
	s := newSigner(t)
	token, _, _ := s.Mint(Identity{UserID: "u-1", Username: "alice"})

	// Flip a character in the signature.
	bad := token[:len(token)-2] + "xy"
	if _, err := s.Verify(bad); err == nil {
		t.Error("expected a tampered signature to be rejected")
	}
}

func TestVerifyRejectsTokenFromAnotherSecret(t *testing.T) {
	s := newSigner(t)
	other, _ := NewSigner("a-completely-different-secret-32bytes!!", time.Hour, false)
	token, _, _ := other.Mint(Identity{UserID: "u-1"})

	if _, err := s.Verify(token); err == nil {
		t.Error("expected a token signed with a different secret to be rejected")
	}
}

// The classic JWT attack: swap the algorithm to "none" and drop the signature.
func TestVerifyRejectsAlgNone(t *testing.T) {
	s := newSigner(t)
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "attacker",
			Issuer:    issuer,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).
		SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(token); err == nil {
		t.Error("expected an alg=none token to be rejected")
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	s, err := NewSigner(testSecret, -time.Minute, false) // already expired
	if err != nil {
		t.Fatal(err)
	}
	// NewSigner clamps non-positive TTLs to the default, so mint an expired token
	// by hand instead.
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "u-1",
			Issuer:    issuer,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(token); err == nil {
		t.Error("expected an expired token to be rejected")
	}
}

func TestVerifyRejectsForeignIssuer(t *testing.T) {
	s := newSigner(t)
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "u-1",
			Issuer:    "somebody-else",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	token, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if _, err := s.Verify(token); err == nil {
		t.Error("expected a token from another issuer to be rejected")
	}
}

func TestMiddlewareAttachesIdentityFromCookie(t *testing.T) {
	s := newSigner(t)
	token, _, _ := s.Mint(Identity{UserID: "u-42", Username: "bob", IsGuest: true})

	var got *Identity
	h := s.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = FromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got == nil || got.UserID != "u-42" || !got.IsGuest {
		t.Errorf("identity from cookie = %+v, want u-42 (guest)", got)
	}
}

func TestMiddlewareAcceptsBearerHeader(t *testing.T) {
	s := newSigner(t)
	token, _, _ := s.Mint(Identity{UserID: "u-7"})

	var got *Identity
	h := s.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = FromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got == nil || got.UserID != "u-7" {
		t.Errorf("identity from bearer = %+v, want u-7", got)
	}
}

// An unusable token must not fail the request: anonymous callers still need to
// reach public queries.
func TestMiddlewarePassesThroughWithoutIdentity(t *testing.T) {
	s := newSigner(t)
	called := false
	h := s.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		called = true
		if _, err := FromContext(r.Context()); err == nil {
			t.Error("expected no identity for a garbage token")
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: "not-a-jwt"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Error("handler was not reached")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestCookieIsHttpOnlyAndSameSite(t *testing.T) {
	s := newSigner(t)
	rec := httptest.NewRecorder()
	s.SetCookie(rec, "tok", time.Now().Add(time.Hour))

	res := rec.Result()
	defer res.Body.Close()
	cookies := res.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	c := cookies[0]
	if !c.HttpOnly {
		t.Error("session cookie must be HttpOnly so scripts cannot read it")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}
}
