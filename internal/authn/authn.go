// Package authn verifies the short-lived signed handoff token a host
// application mints after its own session auth and entitlement checks have
// already passed (PLAN.md §2). It does not replace those checks and does
// not implement a login flow of its own - it only answers "did the host
// actually vouch for this request, for this org, and is that vouch still
// fresh."
package authn

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Audience is the fixed `aud` claim a handoff token must carry. This is
// what stops the platform's own longer-lived session JWT (or any other
// HS256 token signed with the same secret) from being replayed here: it
// was never minted for this audience, so it's rejected even though the
// signature would otherwise verify.
const Audience = "baitbet"

// Claims is the minimal set this service actually uses. The platform may
// sign additional fields; everything else is ignored.
type Claims struct {
	OrgID string `json:"org_id"`
	Role  string `json:"role"`
	jwt.RegisteredClaims
}

var (
	ErrMissingToken = errors.New("authn: missing Authorization header")
	ErrNoOrgID      = errors.New("authn: token has no org_id claim")
)

// Verify checks signature, expiry, and audience, and returns the claims.
// It only accepts HS256 - golang-jwt's WithValidMethods closes the classic
// "alg: none" / algorithm-confusion attack by refusing to even attempt
// verification with any other algorithm the token header might claim.
func Verify(tokenString, secret string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		return []byte(secret), nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience(Audience))
	if err != nil {
		return nil, fmt.Errorf("authn: invalid token: %w", err)
	}
	if !token.Valid {
		return nil, errors.New("authn: token failed validation")
	}
	if claims.OrgID == "" {
		return nil, ErrNoOrgID
	}
	return claims, nil
}

// FromHeader extracts and verifies the bearer token from an incoming
// request's Authorization header.
func FromHeader(r *http.Request, secret string) (*Claims, error) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return nil, ErrMissingToken
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return nil, fmt.Errorf("authn: Authorization header is not a Bearer token")
	}
	return Verify(strings.TrimPrefix(h, prefix), secret)
}

// Mint is used only by local tooling/tests standing in for the platform
// (which is the real token issuer in production - see PLAN.md §2). It is
// not called anywhere in the service's own request path.
func Mint(secret, orgID, userID, role string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		OrgID: orgID,
		Role:  role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			Audience:  jwt.ClaimStrings{Audience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}
