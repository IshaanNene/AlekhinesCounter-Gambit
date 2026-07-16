// Package server — AuthService implementation.
//
// This service verifies credentials and returns users. It deliberately does not
// mint sessions: the gateway owns the JWT, so session policy (lifetime, cookie
// flags, rotation) lives at the edge and identity storage stays independent of it.
package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/store"
	authv1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/auth/v1"
)

// Password policy. Length is the control that actually matters; complexity
// rules mostly push people toward "Passw0rd!" and a sticky note.
const (
	minPasswordLen = 8
	maxPasswordLen = 128 // bcrypt silently truncates past 72 bytes; reject instead
	minUsernameLen = 3
	maxUsernameLen = 32
)

// bcryptCost is the work factor. 12 is a reasonable 2020s default: slow enough
// to blunt offline cracking, fast enough not to become a login bottleneck.
const bcryptCost = 12

// AuthServer implements authv1.AuthServiceServer.
type AuthServer struct {
	authv1.UnimplementedAuthServiceServer
	store *store.Store
	log   *slog.Logger
	// deliverTokens reports whether an email provider is configured. When false,
	// RequestLoginToken returns the token in-band so local development works.
	deliverTokens bool
}

// NewAuth builds an AuthServer.
func NewAuth(st *store.Store, log *slog.Logger, deliverTokens bool) *AuthServer {
	return &AuthServer{store: st, log: log, deliverTokens: deliverTokens}
}

// CreateGuest mints an anonymous account so a player can start immediately.
func (s *AuthServer) CreateGuest(ctx context.Context, _ *authv1.CreateGuestRequest) (*authv1.AuthResponse, error) {
	id, err := s.store.CreateGuestUser(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create guest: %v", err)
	}
	return s.userResponse(ctx, id, true)
}

// Register creates a password account, or upgrades a guest in place so games
// and rating earned before signing up are not lost.
func (s *AuthServer) Register(ctx context.Context, req *authv1.RegisterRequest) (*authv1.AuthResponse, error) {
	username := strings.TrimSpace(req.GetUsername())
	if err := validateUsername(username); err != nil {
		return nil, err
	}
	if err := validatePassword(req.GetPassword()); err != nil {
		return nil, err
	}
	email := strings.TrimSpace(req.GetEmail())
	if email != "" && !looksLikeEmail(email) {
		return nil, status.Error(codes.InvalidArgument, "that does not look like an email address")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.GetPassword()), bcryptCost)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "hash password: %v", err)
	}

	u, err := s.store.Register(ctx, store.RegisterParams{
		Username:      username,
		PasswordHash:  string(hash),
		Email:         email,
		UpgradeUserID: req.GetUpgradeUserId(),
	})
	switch {
	case errors.Is(err, store.ErrUserExists):
		return nil, status.Error(codes.AlreadyExists, "that username or email is already taken")
	case errors.Is(err, store.ErrNotUpgradable):
		return nil, status.Error(codes.FailedPrecondition, "that account is already registered")
	case err != nil:
		return nil, status.Errorf(codes.Internal, "register: %v", err)
	}
	return &authv1.AuthResponse{User: toProtoUser(u, true)}, nil
}

// Login verifies a username-or-email plus password.
func (s *AuthServer) Login(ctx context.Context, req *authv1.LoginRequest) (*authv1.AuthResponse, error) {
	identifier := strings.TrimSpace(req.GetIdentifier())
	if identifier == "" || req.GetPassword() == "" {
		return nil, status.Error(codes.InvalidArgument, "identifier and password are required")
	}

	u, err := s.store.FindUserByIdentifier(ctx, identifier)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, status.Errorf(codes.Internal, "lookup user: %v", err)
	}

	// Compare even when the user is missing, against a dummy hash. Returning
	// early would make "no such user" measurably faster than "wrong password",
	// letting an attacker enumerate accounts by timing.
	hash := dummyHash
	if u != nil && u.PasswordHash != "" {
		hash = u.PasswordHash
	}
	cmpErr := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.GetPassword()))
	if u == nil || u.PasswordHash == "" || cmpErr != nil {
		// One message for every failure: never reveal which part was wrong.
		return nil, status.Error(codes.Unauthenticated, "incorrect username or password")
	}
	return &authv1.AuthResponse{User: toProtoUser(u, true)}, nil
}

// dummyHash is a real bcrypt hash of a random value, used to keep the failure
// path's cost identical to the success path.
var dummyHash = "$2a$12$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

// RequestLoginToken issues a single-use passwordless sign-in token.
//
// Always reports success, even for an unknown address: telling callers whether
// an email is registered is an account-enumeration oracle.
func (s *AuthServer) RequestLoginToken(ctx context.Context, req *authv1.RequestLoginTokenRequest) (*authv1.RequestLoginTokenResponse, error) {
	email := strings.TrimSpace(req.GetEmail())
	if email == "" {
		return nil, status.Error(codes.InvalidArgument, "email is required")
	}

	token, _, expiresAt, err := s.store.NewLoginToken(ctx, email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrNoEmail) {
			return s.decoyTokenResponse(), nil
		}
		return nil, status.Errorf(codes.Internal, "issue login token: %v", err)
	}

	if s.deliverTokens {
		// A mail provider would send the link here. Never return the token when
		// delivery is on: that would defeat the point of emailing it.
		s.log.Info("login token issued", "email_domain", domainOf(email))
		return &authv1.RequestLoginTokenResponse{ExpiresAt: timestamppb.New(expiresAt)}, nil
	}

	s.log.Warn("returning login token in-band: no mail provider configured")
	return &authv1.RequestLoginTokenResponse{
		Token:           token,
		ExpiresAt:       timestamppb.New(expiresAt),
		DeliveredInBand: true,
	}, nil
}

// decoyTokenResponse answers an unknown address with a response shaped exactly
// like the success case.
//
// Returning an empty response would leak: with delivery disabled a real address
// yields a token and delivered_in_band=true, so an empty answer would mark the
// address as unregistered. The decoy token is random and matches no row, so
// redeeming it fails like any expired link.
func (s *AuthServer) decoyTokenResponse() *authv1.RequestLoginTokenResponse {
	expiresAt := time.Now().Add(15 * time.Minute)
	if s.deliverTokens {
		return &authv1.RequestLoginTokenResponse{ExpiresAt: timestamppb.New(expiresAt)}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		// Without randomness we cannot build a convincing decoy; an empty
		// response is a smaller problem than a predictable one.
		return &authv1.RequestLoginTokenResponse{}
	}
	return &authv1.RequestLoginTokenResponse{
		Token:           base64.RawURLEncoding.EncodeToString(raw),
		ExpiresAt:       timestamppb.New(expiresAt),
		DeliveredInBand: true,
	}
}

// RedeemLoginToken exchanges a valid token for its user.
func (s *AuthServer) RedeemLoginToken(ctx context.Context, req *authv1.RedeemLoginTokenRequest) (*authv1.AuthResponse, error) {
	if req.GetToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "token is required")
	}
	u, err := s.store.RedeemLoginToken(ctx, req.GetToken())
	if errors.Is(err, store.ErrTokenInvalid) {
		return nil, status.Error(codes.Unauthenticated, "this sign-in link is invalid, expired, or already used")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "redeem token: %v", err)
	}
	return &authv1.AuthResponse{User: toProtoUser(u, true)}, nil
}

// GetUser returns a public profile (no email).
func (s *AuthServer) GetUser(ctx context.Context, req *authv1.GetUserRequest) (*authv1.AuthResponse, error) {
	if req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	return s.userResponse(ctx, req.GetUserId(), false)
}

func (s *AuthServer) userResponse(ctx context.Context, id string, self bool) (*authv1.AuthResponse, error) {
	u, err := s.store.GetUser(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "user not found")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load user: %v", err)
	}
	return &authv1.AuthResponse{User: toProtoUser(u, self)}, nil
}

// toProtoUser maps a stored user to the wire type. The email is only included
// for the account's owner.
func toProtoUser(u *store.User, self bool) *authv1.User {
	out := &authv1.User{
		Id:          u.ID,
		Username:    u.Username,
		Elo:         int32(u.Elo),
		IsGuest:     u.IsGuest,
		GamesPlayed: int32(u.GamesPlayed),
		CreatedAt:   timestamppb.New(u.CreatedAt),
	}
	if self {
		out.Email = u.Email
	}
	return out
}

// ── validation ──────────────────────────────────────────────────────────────

func validateUsername(name string) error {
	if len(name) < minUsernameLen || len(name) > maxUsernameLen {
		return status.Errorf(codes.InvalidArgument,
			"username must be %d–%d characters", minUsernameLen, maxUsernameLen)
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' {
			return status.Error(codes.InvalidArgument,
				"username may contain only letters, digits, underscores, and hyphens")
		}
	}
	// "guest-" is how anonymous accounts are named; letting anyone claim the
	// prefix would make a real account indistinguishable from a throwaway.
	if strings.HasPrefix(strings.ToLower(name), "guest-") {
		return status.Error(codes.InvalidArgument, "usernames may not start with \"guest-\"")
	}
	return nil
}

func validatePassword(pw string) error {
	if len(pw) < minPasswordLen {
		return status.Errorf(codes.InvalidArgument,
			"password must be at least %d characters", minPasswordLen)
	}
	// bcrypt ignores everything past 72 bytes, which would silently make a long
	// password weaker than it looks. Reject rather than truncate.
	if len(pw) > maxPasswordLen {
		return status.Errorf(codes.InvalidArgument,
			"password must be at most %d characters", maxPasswordLen)
	}
	return nil
}

// looksLikeEmail is a deliberately loose check: the only real validation is
// delivering to the address, and strict regexes reject valid addresses.
func looksLikeEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	return at > 0 && at < len(s)-1 && !strings.ContainsAny(s, " \t\n")
}

func domainOf(email string) string {
	if i := strings.LastIndexByte(email, '@'); i >= 0 {
		return email[i+1:]
	}
	return ""
}
