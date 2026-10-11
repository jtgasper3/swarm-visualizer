package oauth

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"math/big"
	"net/http"
	"strings"
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

// maxMetadataBytes bounds how much of an identity provider response is read.
// Discovery documents and key sets are a few kilobytes.
const maxMetadataBytes = 1 << 20

// jwk holds the members of a JSON Web Key that the store reads.
type jwk struct {
	Kty string   `json:"kty"`
	Use string   `json:"use"`
	Kid string   `json:"kid"`
	N   string   `json:"n"`
	E   string   `json:"e"`
	X5c []string `json:"x5c"`
}

// rsaPublicKey returns the RSA signing key a JWK describes. The modulus and
// exponent are the key itself, and every RSA key is required to publish them.
// x5c is an optional certificate for the same key, which some providers
// include and others leave out, so it is read only when they are absent.
func (k jwk) rsaPublicKey() (*rsa.PublicKey, error) {
	if k.Use != "" && k.Use != "sig" {
		return nil, fmt.Errorf("not a signing key (use %q)", k.Use)
	}
	if k.Kty != "" && k.Kty != "RSA" {
		return nil, fmt.Errorf("not an RSA key (kty %q)", k.Kty)
	}

	if k.N != "" || k.E != "" {
		n, err := decodeJWKInt(k.N)
		if err != nil {
			return nil, fmt.Errorf("invalid modulus: %v", err)
		}
		e, err := decodeJWKInt(k.E)
		if err != nil {
			return nil, fmt.Errorf("invalid exponent: %v", err)
		}
		// An RSA public exponent is odd and small; the usual one is 65537.
		if !e.IsInt64() || e.Int64() < 3 || e.Int64() > math.MaxInt32 || e.Bit(0) == 0 {
			return nil, fmt.Errorf("invalid exponent %s", e)
		}
		return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
	}

	if len(k.X5c) == 0 {
		return nil, errors.New("no key material (neither n and e nor x5c)")
	}
	certData, err := base64.StdEncoding.DecodeString(k.X5c[0])
	if err != nil {
		return nil, fmt.Errorf("failed to decode certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(certData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %v", err)
	}
	rsaPub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("not an RSA public key")
	}
	return rsaPub, nil
}

// decodeJWKInt decodes the unsigned big-endian, base64url integer that a JWK
// uses for n and e.
func decodeJWKInt(value string) (*big.Int, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(value, "="))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, errors.New("empty")
	}
	return new(big.Int).SetBytes(raw), nil
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

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch JWKS: %s returned %s", ks.jwksURI, resp.Status)
	}

	var jwks struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxMetadataBytes)).Decode(&jwks); err != nil {
		return fmt.Errorf("failed to decode JWKS: %v", err)
	}

	newKeys := make(map[string]*rsa.PublicKey, len(jwks.Keys))
	for _, key := range jwks.Keys {
		rsaPub, err := key.rsaPublicKey()
		if err != nil {
			log.Printf("Skipping JWKS key %s: %v", key.Kid, err)
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
