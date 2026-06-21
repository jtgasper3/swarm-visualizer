package oauth

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

const (
	// jwksRefreshInterval is how often signing keys are proactively re-fetched
	// so the server keeps working across IdP key rotations.
	jwksRefreshInterval = time.Hour
	// jwksRefreshThrottle is the minimum spacing between on-demand refreshes
	// triggered by an unknown key id, so a flood of bogus kids can't hammer
	// the identity provider.
	jwksRefreshThrottle = time.Minute
)

// keyStore holds the identity provider's RSA signing keys and keeps them
// current. It is safe for concurrent use.
type keyStore struct {
	jwksURI    string
	httpClient *http.Client

	mu          sync.RWMutex
	keys        map[string]*rsa.PublicKey
	lastRefresh time.Time
}

// newKeyStore creates a key store for the given JWKS endpoint. Callers must
// invoke refresh once to populate it before validating tokens.
func newKeyStore(jwksURI string, httpClient *http.Client) *keyStore {
	return &keyStore{
		jwksURI:    jwksURI,
		httpClient: httpClient,
		keys:       make(map[string]*rsa.PublicKey),
	}
}

// key returns the RSA public key for the given key id. If the kid is unknown
// (e.g. the IdP rotated its keys) it triggers a throttled refresh and retries
// once.
func (ks *keyStore) key(kid string) (*rsa.PublicKey, bool) {
	ks.mu.RLock()
	k, ok := ks.keys[kid]
	ks.mu.RUnlock()
	if ok {
		return k, true
	}

	if !ks.refreshIfStale() {
		return nil, false
	}

	ks.mu.RLock()
	k, ok = ks.keys[kid]
	ks.mu.RUnlock()
	return k, ok
}

// refreshIfStale refreshes the JWKS only if the last attempt was more than
// jwksRefreshThrottle ago. It reports whether a refresh was attempted.
func (ks *keyStore) refreshIfStale() bool {
	ks.mu.RLock()
	fresh := time.Since(ks.lastRefresh) < jwksRefreshThrottle
	ks.mu.RUnlock()
	if fresh {
		return false
	}
	if err := ks.refresh(); err != nil {
		log.Printf("JWKS on-demand refresh failed: %v", err)
	}
	return true
}

// refresh fetches the JWKS and atomically replaces the in-memory key set.
func (ks *keyStore) refresh() error {
	// Record the attempt up front so failures are throttled too.
	ks.mu.Lock()
	ks.lastRefresh = time.Now()
	ks.mu.Unlock()

	resp, err := ks.httpClient.Get(ks.jwksURI)
	if err != nil {
		return fmt.Errorf("failed to fetch JWKS: %v", err)
	}
	defer resp.Body.Close()

	var jwks struct {
		Keys []struct {
			Kid string   `json:"kid"`
			X5c []string `json:"x5c"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("failed to decode JWKS: %v", err)
	}

	newKeys := make(map[string]*rsa.PublicKey, len(jwks.Keys))
	for _, key := range jwks.Keys {
		if len(key.X5c) == 0 {
			log.Printf("Skipping JWKS key %s: no x5c certificate data", key.Kid)
			continue
		}
		certData, err := base64.StdEncoding.DecodeString(key.X5c[0])
		if err != nil {
			log.Printf("Skipping JWKS key %s: failed to decode certificate: %v", key.Kid, err)
			continue
		}
		cert, err := x509.ParseCertificate(certData)
		if err != nil {
			log.Printf("Skipping JWKS key %s: failed to parse certificate: %v", key.Kid, err)
			continue
		}
		rsaPub, ok := cert.PublicKey.(*rsa.PublicKey)
		if !ok {
			log.Printf("Skipping JWKS key %s: not an RSA public key", key.Kid)
			continue
		}
		newKeys[key.Kid] = rsaPub
	}

	if len(newKeys) == 0 {
		return fmt.Errorf("JWKS contained no usable RSA keys")
	}

	ks.mu.Lock()
	ks.keys = newKeys
	ks.mu.Unlock()
	return nil
}

// refreshLoop periodically refreshes the JWKS for the life of the process.
func (ks *keyStore) refreshLoop() {
	ticker := time.NewTicker(jwksRefreshInterval)
	defer ticker.Stop()
	for range ticker.C {
		if err := ks.refresh(); err != nil {
			log.Printf("JWKS periodic refresh failed: %v", err)
		}
	}
}
