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

	"github.com/jtgasper3/swarm-visualizer/internal/docker"
	"github.com/jtgasper3/swarm-visualizer/internal/server"
	"github.com/jtgasper3/swarm-visualizer/internal/shared"
)

const (
	defaultContextRoot  = "/"
	defaultListenerPort = "8080"
)

func main() {
	shared.ClusterName = os.Getenv("CLUSTER_NAME")

	contextRoot, listenerPort := getServerConfig()
	log.Printf("Server root context is %s", contextRoot)

	go docker.InspectSwarmServices()

	fs := http.FileServer(http.Dir("./static"))
	http.Handle(contextRoot, http.StripPrefix(contextRoot, fs))
	http.HandleFunc(contextRoot+"ws", server.HandleConnections)

	if os.Getenv("ENABLE_AUTH") == "true" {
		shared.AuthEnabled = true
	}

	if shared.AuthEnabled {
		shared.OAuthConfig = server.SetupOAuthConfig()

		// Fetch and parse the well-known oidc config
		err := fetchWellKnownOIDCConfig()
		if err != nil {
			log.Fatalf("Failed to fetch JWKS: %v", err)
		}

		http.HandleFunc("/login", server.HandleLogin)
		http.HandleFunc("/callback", server.HandleCallback)
	}

	go server.HandleMessages()

	log.Printf("Server started on :%s", listenerPort)
	err := http.ListenAndServe(":"+listenerPort, nil)
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

func fetchWellKnownOIDCConfig() error {
	wellKnownURL := os.Getenv("OIDC_WELL_KNOWN_URL")
	resp, err := http.Get(wellKnownURL)
	if err != nil {
		return fmt.Errorf("failed to fetch well-known configuration: %v", err)
	}
	defer resp.Body.Close()

	var config struct {
		TokenUrl string `json:"token_endpoint"`
		AuthUrl  string `json:"authorization_endpoint"`
		JWKSURI  string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return fmt.Errorf("failed to decode well-known configuration: %v", err)
	}

	if shared.OAuthConfig.Endpoint.AuthURL == "" {
		log.Printf("Using Authorization Endpoint from well-known config %s", config.AuthUrl)
		shared.OAuthConfig.Endpoint.AuthURL = config.AuthUrl
	}
	if shared.OAuthConfig.Endpoint.TokenURL == "" {
		log.Printf("Using Token Endpoint from well-known config %s", config.TokenUrl)
		shared.OAuthConfig.Endpoint.TokenURL = config.TokenUrl
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

		shared.RsaPublicKeyMap[key.Kid] = cert.PublicKey.(*rsa.PublicKey)
	}
	return nil
}
