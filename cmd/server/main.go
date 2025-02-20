package main

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/dgrijalva/jwt-go"
	"github.com/jtgasper3/swarm-visualizer/internal/docker"
	"github.com/jtgasper3/swarm-visualizer/internal/models"
	"github.com/jtgasper3/swarm-visualizer/internal/server"
	"github.com/jtgasper3/swarm-visualizer/internal/shared"
)

const (
	defaultContextRoot  = "/"
	defaultListenerPort = "8080"
)

var rsaPublicKeyMap = make(map[string]*rsa.PublicKey)

func main() {
	shared.ClusterName = os.Getenv("CLUSTER_NAME")

	contextRoot, listenerPort := getServerConfig()
	log.Printf("Server root context is %s", contextRoot)

	go docker.InspectSwarmServices()

	fs := http.FileServer(http.Dir("./static"))
	http.Handle(contextRoot, http.StripPrefix(contextRoot, fs))
	http.HandleFunc(contextRoot+"ws", server.HandleConnections)

	shared.OAuthConfig = server.SetupOAuthConfig()
	// Fetch and parse the JWKS
	err := fetchJWKS()
	if err != nil {
		log.Fatalf("Failed to fetch JWKS: %v", err)
	}

	http.HandleFunc("/login", server.HandleLogin)
	http.HandleFunc("/callback", server.HandleCallback)
	http.HandleFunc("/protected", handleProtected)

	go server.HandleMessages()

	log.Printf("Server started on :%s", listenerPort)
	err = http.ListenAndServe(":"+listenerPort, nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}

func getServerConfig() (string, string) {
	contextRoot := os.Getenv("CONTEXT_ROOT")
	if contextRoot == "" {
		contextRoot = defaultContextRoot
	}
	if !strings.HasSuffix(contextRoot, "/") {
		contextRoot += "/"
	}

	listenerPort := os.Getenv("LISTENER_PORT")
	if listenerPort == "" {
		listenerPort = defaultListenerPort
	}

	return contextRoot, listenerPort
}

func handleProtected(w http.ResponseWriter, r *http.Request) {
	var rawIDToken string
	cookie, err := r.Cookie("id_token")
	if err != nil {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			rawIDToken = strings.TrimPrefix(authHeader, "Bearer ")
		} else {
			http.Error(w, "Unauthorized: No valid ID token", http.StatusUnauthorized)
			return
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

		rsaPublicKey, ok := rsaPublicKeyMap[kid]
		if !ok {
			return nil, fmt.Errorf("unknown kid: %s", kid)
		}

		return rsaPublicKey, nil
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to parse ID token: %v", err), http.StatusUnauthorized)
		return
	}

	if claims, ok := token.Claims.(*models.IDTokenClaims); ok && token.Valid {
		if claims.Audience != shared.OAuthConfig.ClientID {
			http.Error(w, fmt.Sprintf("ID token for a different token: %s", claims.Audience), http.StatusUnauthorized)
		}

		response := map[string]interface{}{
			"message": "Protected endpoint accessed!",
			"claims":  claims,
		}
		json.NewEncoder(w).Encode(response)
	} else {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}
}

func fetchJWKS() error {
	wellKnownURL := "https://login.microsoftonline.com/446b4c6b-376e-4a2e-bcd8-b6284b57b6ca/v2.0/.well-known/openid-configuration"
	resp, err := http.Get(wellKnownURL)
	if err != nil {
		return fmt.Errorf("failed to fetch well-known configuration: %v", err)
	}
	defer resp.Body.Close()

	var config struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return fmt.Errorf("failed to decode well-known configuration: %v", err)
	}

	jwksResp, err := http.Get(config.JWKSURI)
	if err != nil {
		return fmt.Errorf("failed to fetch JWKS: %v", err)
	}
	defer jwksResp.Body.Close()

	var jwks struct {
		Keys []struct {
			Kty string   `json:"kty"`
			Kid string   `json:"kid"`
			Use string   `json:"use"`
			N   string   `json:"n"`
			E   string   `json:"e"`
			X5c []string `json:"x5c"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(jwksResp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("failed to decode JWKS: %v", err)
	}

	for _, key := range jwks.Keys {
		certData, err := base64.StdEncoding.DecodeString(key.X5c[0])
		if err != nil {
			return fmt.Errorf("failed to decode certificate: %v", err)
		}

		cert, err := x509.ParseCertificate(certData)
		if err != nil {
			return fmt.Errorf("failed to parse certificate: %v", err)
		}

		rsaPublicKeyMap[key.Kid] = cert.PublicKey.(*rsa.PublicKey)
	}
	return nil
}
