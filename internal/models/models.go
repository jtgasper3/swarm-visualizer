package models

import "time"

type NodeViewModel struct {
	ID                   string `json:"id"`
	Hostname             string `json:"hostname"`
	Name                 string `json:"name"`
	Role                 string `json:"role"`
	PlatformArchitecture string `json:"platformArchitecture"`
	MemoryBytes          int64  `json:"memoryBytes"`
	Availability         string `json:"availability"`
	Status               string `json:"status"`
}

type ServiceViewModel struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Image string `json:"image"`
	Mode  string `json:"mode"`
}

type TaskViewModel struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	NodeID       string    `json:"nodeId"`
	ServiceID    string    `json:"serviceId"`
	DesiredState string    `json:"desiredState"`
	State        string    `json:"state"`
	CreatedAt    time.Time `json:"createdAt"`
}

type SwarmData struct {
	ClusterName string             `json:"clusterName"`
	Nodes       []NodeViewModel    `json:"nodes"`
	Services    []ServiceViewModel `json:"services"`
	Tasks       []TaskViewModel    `json:"tasks"`
}
