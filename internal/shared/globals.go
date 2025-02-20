package shared

import (
	"sync"

	"github.com/jtgasper3/swarm-visualizer/internal/models"
)

var (
	ClusterName         string
	Broadcast           = make(chan []byte)
	LastBroadcastedData *models.SwarmData
	Mu                  sync.Mutex
)
