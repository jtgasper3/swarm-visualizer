package shared

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/dgrijalva/jwt-go"
	"github.com/jtgasper3/swarm-visualizer/internal/models"
)

func ValidateToken(r *http.Request) (*models.IDTokenClaims, error) {
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

	token, err := jwt.ParseWithClaims(rawIDToken, &models.IDTokenClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		kid, ok := token.Header["kid"].(string)
		if !ok {
			return nil, fmt.Errorf("missing kid in token header")
		}

		rsaPublicKey, ok := RsaPublicKeyMap[kid]
		if !ok {
			return nil, fmt.Errorf("unknown kid: %s", kid)
		}

		return rsaPublicKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to parse ID token: %v", err)
	}

	claims, ok := token.Claims.(*models.IDTokenClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("unauthorized")
	}

	if claims.Audience != OAuthConfig.ClientID {
		return nil, fmt.Errorf("ID token for a different audience: %s", claims.Audience)
	}

	return claims, nil
}
