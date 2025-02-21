package shared

import (
	"crypto/rsa"
	"sync"

	"github.com/jtgasper3/swarm-visualizer/internal/models"
	"golang.org/x/oauth2"
)

var (
	ClusterName         string
	Broadcast           = make(chan []byte)
	LastBroadcastedData *models.SwarmData
	Mu                  sync.Mutex
	AuthEnabled         = false
	OAuthConfig         *oauth2.Config
	RsaPublicKeyMap     = make(map[string]*rsa.PublicKey)
)
