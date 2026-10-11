package oauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jtgasper3/swarm-visualizer/internal/config"
)

const (
	// sessionCookie holds the session issued after a successful login.
	sessionCookie = "swarm_visualizer_session"
	// sessionAudience marks a token as one of these sessions, so that nothing
	// else signed with the same key could be presented as one.
	sessionAudience = "swarm-visualizer-session"
	// usernameKey is where a session keeps the user's name, whatever claim the
	// identity provider supplied it under.
	usernameKey = "username"
)

// newSessionKey returns the key that signs session cookies.
//
// It is derived from the client secret, so every replica arrives at the same
// key and a session survives a restart, with no further secret to manage. The
// issuer, client ID and redirect URL go into it as well: they are what make
// this deployment this one, so another deployment that happens to share the
// secret, or the whole client registration, does not accept these sessions.
//
// A deployment that has no client secret gets a random key instead. Its
// sessions last only as long as the process and are not recognized by other
// replicas.
func newSessionKey(cfg *config.OAuthConfig) ([]byte, error) {
	if cfg.ClientSecret == "" {
		key := make([]byte, sha256.Size)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("failed to generate a session key: %w", err)
		}
		log.Printf("No OIDC client secret is configured; sessions will not survive a restart or be shared between replicas")
		return key, nil
	}

	mac := hmac.New(sha256.New, []byte(cfg.ClientSecret))
	for _, part := range []string{"swarm-visualizer session cookie v1", cfg.Issuer, cfg.ClientID, cfg.RedirectURL} {
		mac.Write([]byte(part))
		mac.Write([]byte{0})
	}
	return mac.Sum(nil), nil
}

// newSession issues the session for a login whose ID token has been validated,
// and returns it with its expiry.
//
// The cookie used to hold the ID token itself. A token with many group or role
// claims exceeds the 4 KB a browser will store for one cookie; the browser
// drops it without a word and the page goes straight back to the login. A
// session carries only the subject, the name to log, and an expiry.
//
// A session ends when the ID token would have, or after SessionMaxAge if that
// comes first, which is how long the token in the cookie was accepted for.
func (a *Authenticator) newSession(idClaims jwt.MapClaims, now time.Time) (string, time.Time, error) {
	if len(a.sessionKey) == 0 {
		return "", time.Time{}, errors.New("session key not initialized")
	}

	expires := now.Add(time.Duration(a.cfg.OAuthConfig.SessionMaxAge) * time.Second)
	if tokenExpiry, err := idClaims.GetExpirationTime(); err == nil && tokenExpiry != nil && tokenExpiry.Before(expires) {
		expires = tokenExpiry.Time
	}

	claims := jwt.MapClaims{
		"aud": sessionAudience,
		"iat": now.Unix(),
		"exp": expires.Unix(),
	}
	if sub, ok := idClaims["sub"]; ok {
		claims["sub"] = sub
	}
	if username, ok := idClaims[a.cfg.OAuthConfig.UsernameClaim]; ok {
		claims[usernameKey] = username
	}

	session, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(a.sessionKey)
	if err != nil {
		return "", time.Time{}, err
	}
	return session, expires, nil
}

// validateSession verifies a session cookie's signature, audience, and expiry
// and returns its claims, with the user's name under the configured claim as
// it would be in an ID token.
func (a *Authenticator) validateSession(session string) (jwt.MapClaims, error) {
	if len(a.sessionKey) == 0 {
		return nil, errors.New("session key not initialized")
	}

	token, err := jwt.Parse(session,
		func(*jwt.Token) (any, error) { return a.sessionKey, nil },
		// Only the algorithm sessions are issued with: an ID token, which is
		// signed by the identity provider, is not a session.
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithAudience(sessionAudience),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, fmt.Errorf("invalid session: %v", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid session")
	}
	if username, ok := claims[usernameKey]; ok {
		claims[a.cfg.OAuthConfig.UsernameClaim] = username
	}
	return claims, nil
}
