package shared

import (
	"sync"

	"github.com/jtgasper3/swarm-visualizer/internal/models"
	"golang.org/x/oauth2"
)

var (
	ClusterName         string
	Broadcast           = make(chan []byte)
	LastBroadcastedData *models.SwarmData
	Mu                  sync.Mutex
	OAuthConfig         *oauth2.Config
)
