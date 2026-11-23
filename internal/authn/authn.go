// Package authn verifies the short-lived signed handoff token a host app
// mints after its own auth has already passed (see ARCHITECTURE.md).
package authn

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Audience is the fixed `aud` claim a handoff token must carry, so a
// token signed for something else (e.g. the host's own session JWT)
// can't be replayed here even with a valid signature.
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

// Verify checks signature, expiry and audience; HS256 only, to close the
// "alg: none" / algorithm-confusion attack.
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

// FromHeader verifies the bearer token in an incoming request.
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

// Mint is for local tooling/tests only; a host app mints tokens in production.
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
