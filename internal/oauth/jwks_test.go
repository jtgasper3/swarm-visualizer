package oauth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jtgasper3/swarm-visualizer/internal/config"
)

// selfSignedDER returns a self-signed certificate (DER bytes) for the given
// public key, signed by the matching private key.
func selfSignedDER(t *testing.T, pub, priv any) []byte {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	return der
}

// rsaX5c returns an RSA public key and its base64 x5c certificate entry.
func rsaX5c(t *testing.T) (*rsa.PublicKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa key: %v", err)
	}
	der := selfSignedDER(t, &key.PublicKey, key)
	return &key.PublicKey, base64.StdEncoding.EncodeToString(der)
}

// ecX5c returns the base64 x5c certificate entry for a non-RSA (EC) key.
func ecX5c(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ec key: %v", err)
	}
	der := selfSignedDER(t, &key.PublicKey, key)
	return base64.StdEncoding.EncodeToString(der)
}

type testJWK struct {
	kid string
	x5c []string
	// kty, use, n and e are published as given; empty ones are left out.
	kty, use, n, e string
}

// rsaNE returns an RSA key with its modulus and exponent encoded as a JWK
// publishes them.
func rsaNE(t *testing.T) (*rsa.PrivateKey, string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa key: %v", err)
	}
	n := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())
	return key, n, e
}

func jwksBody(t *testing.T, keys ...testJWK) string {
	t.Helper()
	type jwk struct {
		Kty string   `json:"kty,omitempty"`
		Use string   `json:"use,omitempty"`
		Kid string   `json:"kid"`
		N   string   `json:"n,omitempty"`
		E   string   `json:"e,omitempty"`
		X5c []string `json:"x5c,omitempty"`
	}
	doc := struct {
		Keys []jwk `json:"keys"`
	}{}
	for _, k := range keys {
		doc.Keys = append(doc.Keys, jwk{Kty: k.kty, Use: k.use, Kid: k.kid, N: k.n, E: k.e, X5c: k.x5c})
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal jwks: %v", err)
	}
	return string(b)
}

// jwksHandler serves a configurable JWKS body and counts the number of fetches.
type jwksHandler struct {
	mu   sync.Mutex
	body string
	hits int
}

func (h *jwksHandler) setBody(b string) {
	h.mu.Lock()
	h.body = b
	h.mu.Unlock()
}

func (h *jwksHandler) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hits
}

func (h *jwksHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.hits++
	b := h.body
	h.mu.Unlock()
	_, _ = io.WriteString(w, b)
}

func TestKeyStore_RefreshParsesRSAKeys(t *testing.T) {
	pub, x5c := rsaX5c(t)
	h := &jwksHandler{body: jwksBody(t, testJWK{kid: "rsa1", x5c: []string{x5c}})}
	srv := httptest.NewServer(h)
	defer srv.Close()

	ks := newKeyStore(srv.URL, srv.Client())
	if err := ks.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	got, ok := ks.key("rsa1")
	if !ok {
		t.Fatal("expected key rsa1 to be present")
	}
	if !got.Equal(pub) {
		t.Fatal("returned key does not match the certificate key")
	}
}

func TestKeyStore_SkipsUnusableKeys(t *testing.T) {
	pub, rsaCert := rsaX5c(t)
	ecCert := ecX5c(t)
	h := &jwksHandler{body: jwksBody(t,
		testJWK{kid: "rsa1", x5c: []string{rsaCert}},
		testJWK{kid: "nocert"},                     // no x5c -> skipped
		testJWK{kid: "ec1", x5c: []string{ecCert}}, // non-RSA -> skipped, not fatal
	)}
	srv := httptest.NewServer(h)
	defer srv.Close()

	ks := newKeyStore(srv.URL, srv.Client())
	if err := ks.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	if got, ok := ks.key("rsa1"); !ok || !got.Equal(pub) {
		t.Fatal("expected usable rsa1 key to be present")
	}
	if _, ok := ks.key("nocert"); ok {
		t.Fatal("key without x5c should be skipped")
	}
	if _, ok := ks.key("ec1"); ok {
		t.Fatal("non-RSA key should be skipped")
	}
	// The lookups above should not have triggered another fetch (throttled).
	if h.count() != 1 {
		t.Fatalf("expected 1 fetch, got %d", h.count())
	}
}

func TestKeyStore_NoUsableKeysReturnsError(t *testing.T) {
	h := &jwksHandler{body: jwksBody(t, testJWK{kid: "ec1", x5c: []string{ecX5c(t)}})}
	srv := httptest.NewServer(h)
	defer srv.Close()

	ks := newKeyStore(srv.URL, srv.Client())
	if err := ks.refresh(); err == nil {
		t.Fatal("expected an error when no usable RSA keys are present")
	}
}

func TestKeyStore_RetainsKeysOnFailedRefresh(t *testing.T) {
	pub, rsaCert := rsaX5c(t)
	h := &jwksHandler{body: jwksBody(t, testJWK{kid: "rsa1", x5c: []string{rsaCert}})}
	srv := httptest.NewServer(h)
	defer srv.Close()

	ks := newKeyStore(srv.URL, srv.Client())
	if err := ks.refresh(); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}

	h.setBody("not valid json")
	if err := ks.refresh(); err == nil {
		t.Fatal("expected decode error on bad refresh")
	}

	if got, ok := ks.key("rsa1"); !ok || !got.Equal(pub) {
		t.Fatal("expected previously loaded key to be retained after a failed refresh")
	}
}

func TestKeyStore_RefreshesOnUnknownKid(t *testing.T) {
	_, cert1 := rsaX5c(t)
	pub2, cert2 := rsaX5c(t)
	h := &jwksHandler{body: jwksBody(t, testJWK{kid: "rsa1", x5c: []string{cert1}})}
	srv := httptest.NewServer(h)
	defer srv.Close()

	ks := newKeyStore(srv.URL, srv.Client())
	if err := ks.refresh(); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}

	// Rotate the published keys, then age the store past the throttle window so
	// an unknown kid triggers an on-demand refresh.
	h.setBody(jwksBody(t, testJWK{kid: "rsa2", x5c: []string{cert2}}))
	ks.mu.Lock()
	ks.lastRefresh = time.Now().Add(-2 * jwksRefreshThrottle)
	ks.mu.Unlock()

	got, ok := ks.key("rsa2")
	if !ok || !got.Equal(pub2) {
		t.Fatal("expected rotated key rsa2 to be picked up via on-demand refresh")
	}
	if h.count() != 2 {
		t.Fatalf("expected 2 fetches (initial + on-demand), got %d", h.count())
	}
}

func TestKeyStore_ThrottlesUnknownKid(t *testing.T) {
	_, cert1 := rsaX5c(t)
	h := &jwksHandler{body: jwksBody(t, testJWK{kid: "rsa1", x5c: []string{cert1}})}
	srv := httptest.NewServer(h)
	defer srv.Close()

	ks := newKeyStore(srv.URL, srv.Client())
	if err := ks.refresh(); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}

	// lastRefresh is recent, so an unknown kid must not trigger another fetch.
	if _, ok := ks.key("missing"); ok {
		t.Fatal("unknown kid should not resolve")
	}
	if h.count() != 1 {
		t.Fatalf("expected on-demand refresh to be throttled, got %d fetches", h.count())
	}
}

// Google, Cognito and others publish the modulus and exponent alone, with no
// x5c certificate.
func TestKeyStore_ParsesModulusAndExponent(t *testing.T) {
	key, n, e := rsaNE(t)
	h := &jwksHandler{body: jwksBody(t, testJWK{kid: "ne1", kty: "RSA", use: "sig", n: n, e: e})}
	srv := httptest.NewServer(h)
	defer srv.Close()

	ks := newKeyStore(srv.URL, srv.Client())
	if err := ks.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	got, ok := ks.key("ne1")
	if !ok {
		t.Fatal("expected key ne1 to be present")
	}
	if !got.Equal(&key.PublicKey) {
		t.Fatal("returned key does not match the published modulus and exponent")
	}

	// A token signed with that key validates end to end.
	a := &Authenticator{
		cfg:  &config.Config{OAuthConfig: config.OAuthConfig{ClientID: "client-1", Issuer: "https://issuer.example.com"}},
		keys: ks,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "https://issuer.example.com", "aud": "client-1", "sub": "user-1", "exp": time.Now().Add(time.Hour).Unix(),
	})
	tok.Header["kid"] = "ne1"
	signed, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := a.validateRawToken(signed); err != nil {
		t.Fatalf("token signed by the published key was rejected: %v", err)
	}
}

func TestKeyStore_KeySelection(t *testing.T) {
	key, n, e := rsaNE(t)
	certKey, cert := rsaX5c(t)
	padded := n + strings.Repeat("=", (4-len(n)%4)%4)
	even := base64.RawURLEncoding.EncodeToString(big.NewInt(65536).Bytes())
	huge := base64.RawURLEncoding.EncodeToString(new(big.Int).Lsh(big.NewInt(1), 40).Bytes())

	h := &jwksHandler{body: jwksBody(t,
		// n and e are the key; a certificate alongside them is not consulted.
		testJWK{kid: "both", n: n, e: e, x5c: []string{cert}},
		testJWK{kid: "padded", n: padded, e: e},
		testJWK{kid: "cert-only", x5c: []string{cert}},
		// Not for verifying signatures.
		testJWK{kid: "encryption", kty: "RSA", use: "enc", n: n, e: e},
		testJWK{kid: "elliptic", kty: "EC", use: "sig"},
		// Unusable key material.
		testJWK{kid: "no-exponent", n: n},
		testJWK{kid: "no-modulus", e: e},
		testJWK{kid: "bad-base64", n: "!!!", e: e},
		testJWK{kid: "even-exponent", n: n, e: even},
		testJWK{kid: "huge-exponent", n: n, e: huge},
		testJWK{kid: "nothing"},
	)}
	srv := httptest.NewServer(h)
	defer srv.Close()

	ks := newKeyStore(srv.URL, srv.Client())
	if err := ks.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	want := map[string]*rsa.PublicKey{"both": &key.PublicKey, "padded": &key.PublicKey, "cert-only": certKey}
	for kid, pub := range want {
		if got, ok := ks.keys[kid]; !ok || !got.Equal(pub) {
			t.Errorf("key %q: present=%v, or not the expected key", kid, ok)
		}
	}
	for kid := range ks.keys {
		if _, ok := want[kid]; !ok {
			t.Errorf("key %q should have been skipped", kid)
		}
	}
}

// An error page is not a key set, even when it happens to be valid JSON.
func TestKeyStore_RejectsErrorStatus(t *testing.T) {
	_, n, e := rsaNE(t)
	body := jwksBody(t, testJWK{kid: "ne1", n: n, e: e})
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	ks := newKeyStore(srv.URL, srv.Client())
	if err := ks.refresh(); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}

	status = http.StatusServiceUnavailable
	err := ks.refresh()
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("error = %v, want one reporting the 503", err)
	}
	if _, ok := ks.keys["ne1"]; !ok {
		t.Error("expected the previously loaded key to be retained")
	}
}
