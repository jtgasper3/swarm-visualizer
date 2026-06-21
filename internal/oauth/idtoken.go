package oauth

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// ValidateToken extracts the ID token from the request (cookie or bearer
// header) and verifies it.
func (a *Authenticator) ValidateToken(r *http.Request) (jwt.MapClaims, error) {
	var rawIDToken string
	cookie, err := r.Cookie("id_token")
	if err != nil {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			rawIDToken = strings.TrimPrefix(authHeader, "Bearer ")
		} else {
			return nil, fmt.Errorf("Unauthorized: No valid ID token")
		}
	} else {
		rawIDToken = cookie.Value
	}

	return a.validateRawToken(rawIDToken)
}

// validateRawToken verifies an ID token's signature, issuer, and audience and
// returns its claims. It is shared by the WebSocket request path and the OAuth
// callback (which additionally checks the nonce).
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
	})
	if err != nil {
		return nil, fmt.Errorf("failed to parse ID token: %v", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("unauthorized")
	}

	if a.cfg.OAuthConfig.Issuer != "" {
		issuer, err := claims.GetIssuer()
		if err != nil {
			return nil, fmt.Errorf("failed to read issuer claim: %v", err)
		}
		if issuer != a.cfg.OAuthConfig.Issuer {
			return nil, fmt.Errorf("ID token from unexpected issuer: %s", issuer)
		}
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
