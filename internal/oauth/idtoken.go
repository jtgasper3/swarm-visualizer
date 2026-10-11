package oauth

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// ValidateToken authenticates a request and returns its claims. A browser
// presents the session cookie issued at login; any other client can present an
// ID token as a bearer token.
func (a *Authenticator) ValidateToken(r *http.Request) (jwt.MapClaims, error) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		return a.validateSession(cookie.Value)
	}

	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		return a.validateRawToken(strings.TrimPrefix(authHeader, "Bearer "))
	}
	return nil, fmt.Errorf("Unauthorized: no session cookie or bearer token")
}

// validateRawToken verifies an ID token's signature, expiry, issuer, and
// audience and returns its claims. It is shared by bearer-token requests and
// the OAuth callback (which additionally checks the nonce).
func (a *Authenticator) validateRawToken(rawIDToken string) (jwt.MapClaims, error) {
	token, err := jwt.Parse(rawIDToken, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		kid, ok := token.Header["kid"].(string)
		if !ok {
			return nil, fmt.Errorf("missing kid in token header")
		}

		if a.keys == nil {
			return nil, fmt.Errorf("signing keys not initialized")
		}
		rsaPublicKey, ok := a.keys.key(kid)
		if !ok {
			return nil, fmt.Errorf("unknown kid: %s", kid)
		}

		return rsaPublicKey, nil
	},
		// The parser checks an expiry only when the token has one. Every
		// identity provider is required to set it, and a token without one
		// would otherwise be good for as long as its signing key is.
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ID token: %v", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("unauthorized")
	}

	issuer, err := claims.GetIssuer()
	if err != nil {
		return nil, fmt.Errorf("failed to read issuer claim: %v", err)
	}
	if issuer == "" || issuer != a.cfg.OAuthConfig.Issuer {
		return nil, fmt.Errorf("ID token from unexpected issuer: %s", issuer)
	}

	audiences, err := claims.GetAudience()
	if err != nil {
		return nil, fmt.Errorf("failed to read audience claim: %v", err)
	}
	validAudience := false
	for _, aud := range audiences {
		if aud == a.cfg.OAuthConfig.ClientID {
			validAudience = true
			break
		}
	}
	if !validAudience {
		return nil, fmt.Errorf("ID token for a different audience: %v", audiences)
	}

	return claims, nil
}
