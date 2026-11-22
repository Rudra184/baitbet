package authn

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test-shared-secret"

func TestMintAndVerify_RoundTrip(t *testing.T) {
	tok, err := Mint(testSecret, "org_1", "user_1", "admin", time.Minute)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	claims, err := Verify(tok, testSecret)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.OrgID != "org_1" {
		t.Fatalf("expected org_1, got %q", claims.OrgID)
	}
}

func TestVerify_RejectsWrongSecret(t *testing.T) {
	tok, _ := Mint(testSecret, "org_1", "user_1", "admin", time.Minute)
	if _, err := Verify(tok, "a-different-secret"); err == nil {
		t.Fatal("expected Verify to reject a token signed with a different secret")
	}
}

func TestVerify_RejectsExpiredToken(t *testing.T) {
	tok, _ := Mint(testSecret, "org_1", "user_1", "admin", -time.Minute) // already expired
	if _, err := Verify(tok, testSecret); err == nil {
		t.Fatal("expected Verify to reject an expired token")
	}
}

func TestVerify_RejectsMissingAudience(t *testing.T) {
	// Simulates the platform's OWN session JWT - signed with the same
	// secret, but never minted with aud=baitbet. Must not be accepted here;
	// otherwise a stolen/logged session token could be replayed as a
	// handoff token.
	claims := jwt.RegisteredClaims{
		Subject:   "user_1",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := Verify(signed, testSecret); err == nil {
		t.Fatal("expected Verify to reject a token with no baitbet audience claim")
	}
}

func TestVerify_RejectsNoneAlgorithm(t *testing.T) {
	// The classic JWT "alg: none" attack: a token claiming to need no
	// signature at all. WithValidMethods must refuse this outright.
	token := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{
		Audience:  jwt.ClaimStrings{Audience},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	})
	signed, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign with none: %v", err)
	}
	if _, err := Verify(signed, testSecret); err == nil {
		t.Fatal("expected Verify to reject an alg:none token")
	}
}

func TestVerify_RejectsMissingOrgID(t *testing.T) {
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user_1",
			Audience:  jwt.ClaimStrings{Audience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := token.SignedString([]byte(testSecret))
	if _, err := Verify(signed, testSecret); err != ErrNoOrgID {
		t.Fatalf("expected ErrNoOrgID, got %v", err)
	}
}

func TestFromHeader(t *testing.T) {
	tok, _ := Mint(testSecret, "org_1", "user_1", "admin", time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, err := FromHeader(req, testSecret); err != ErrMissingToken {
		t.Fatalf("expected ErrMissingToken with no header, got %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+tok)
	claims, err := FromHeader(req, testSecret)
	if err != nil {
		t.Fatalf("FromHeader: %v", err)
	}
	if claims.OrgID != "org_1" {
		t.Fatalf("expected org_1, got %q", claims.OrgID)
	}

	req.Header.Set("Authorization", tok) // missing "Bearer " prefix
	if _, err := FromHeader(req, testSecret); err == nil {
		t.Fatal("expected an error for a header missing the Bearer prefix")
	}
}
