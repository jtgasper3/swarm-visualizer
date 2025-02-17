package main

import (
	"sync"
)

var (
	clusterName         string
	lastBroadcastedData *SwarmData
	mu                  sync.Mutex
)
