package oauth

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jtgasper3/swarm-visualizer/internal/config"
)

func ValidateToken(cfg *config.Config, r *http.Request) (jwt.MapClaims, error) {
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

	token, err := jwt.Parse(rawIDToken, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		kid, ok := token.Header["kid"].(string)
		if !ok {
			return nil, fmt.Errorf("missing kid in token header")
		}

		rsaPublicKey, ok := cfg.OAuthConfig.RsaPublicKeyMap[kid]
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

	if claims["aud"] != cfg.OAuthConfig.ClientID {
		return nil, fmt.Errorf("ID token for a different audience: %s", claims["aud"])
	}

	return claims, nil
}
