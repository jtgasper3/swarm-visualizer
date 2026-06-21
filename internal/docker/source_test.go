package docker

import (
	"context"
	"testing"
	"time"

	"github.com/jtgasper3/swarm-visualizer/internal/config"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/swarm"
)

// fakeSource is a swarmSource backed by canned data for tests.
type fakeSource struct {
	nodes    []swarm.Node
	services []swarm.Service
	tasks    []swarm.Task
	networks []network.Summary
	err      error
}

func (f fakeSource) Nodes(context.Context) ([]swarm.Node, error)         { return f.nodes, f.err }
func (f fakeSource) Services(context.Context) ([]swarm.Service, error)   { return f.services, f.err }
func (f fakeSource) Tasks(context.Context) ([]swarm.Task, error)         { return f.tasks, f.err }
func (f fakeSource) Networks(context.Context) ([]network.Summary, error) { return f.networks, f.err }

func taskIDs(tasks []swarm.Task) map[string]bool {
	ids := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		ids[t.ID] = true
	}
	return ids
}

func TestGetTasksInfo_FiltersRunningAndRecentlyStopped(t *testing.T) {
	stoppedTaskCache = make(map[string]cachedTask)
	t.Cleanup(func() { stoppedTaskCache = make(map[string]cachedTask) })

	now := time.Now()
	src := fakeSource{tasks: []swarm.Task{
		// Running task: always included.
		{ID: "run1", ServiceID: "s1", Slot: 1, DesiredState: swarm.TaskStateRunning,
			Status: swarm.TaskStatus{State: swarm.TaskStateRunning}},
		// Recently failed task: included via the stopped-task grace period.
		{ID: "fail1", ServiceID: "s1", Slot: 2, DesiredState: swarm.TaskStateShutdown,
			Status: swarm.TaskStatus{State: swarm.TaskStateFailed}, Meta: swarm.Meta{UpdatedAt: now}},
		// Long-stopped task: excluded as historical.
		{ID: "old1", ServiceID: "s1", Slot: 3, DesiredState: swarm.TaskStateShutdown,
			Status: swarm.TaskStatus{State: swarm.TaskStateComplete}, Meta: swarm.Meta{UpdatedAt: now.Add(-time.Hour)}},
	}}

	out, err := getTasksInfo(context.Background(), src, &config.Config{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ids := taskIDs(out)
	if !ids["run1"] {
		t.Error("expected running task run1 to be included")
	}
	if !ids["fail1"] {
		t.Error("expected recently-failed task fail1 to be included")
	}
	if ids["old1"] {
		t.Error("expected long-stopped task old1 to be excluded")
	}
}

func TestGetTasksInfo_NewestStoppedPerSlotWins(t *testing.T) {
	stoppedTaskCache = make(map[string]cachedTask)
	t.Cleanup(func() { stoppedTaskCache = make(map[string]cachedTask) })

	now := time.Now()
	src := fakeSource{tasks: []swarm.Task{
		// Two failed tasks for the same service+slot; only the newest should show.
		{ID: "old", ServiceID: "s1", Slot: 1, DesiredState: swarm.TaskStateShutdown,
			Status: swarm.TaskStatus{State: swarm.TaskStateFailed}, Meta: swarm.Meta{UpdatedAt: now, CreatedAt: now.Add(-2 * time.Minute)}},
		{ID: "new", ServiceID: "s1", Slot: 1, DesiredState: swarm.TaskStateShutdown,
			Status: swarm.TaskStatus{State: swarm.TaskStateFailed}, Meta: swarm.Meta{UpdatedAt: now, CreatedAt: now.Add(-1 * time.Minute)}},
	}}

	out, err := getTasksInfo(context.Background(), src, &config.Config{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ids := taskIDs(out)
	if !ids["new"] {
		t.Error("expected the newest stopped task in the slot to be included")
	}
	if ids["old"] {
		t.Error("expected the older stopped task in the same slot to be excluded")
	}
}

func TestGetNetworksInfo_RemovesIngressAndHidesLabels(t *testing.T) {
	src := fakeSource{networks: []network.Summary{
		{Network: network.Network{Name: "ingress", Labels: map[string]string{"k": "v"}}},
		{Network: network.Network{Name: "app", Labels: map[string]string{"k": "v"}}},
	}}

	// Default config: ingress dropped, labels retained.
	out, err := getNetworksInfo(context.Background(), src, &config.Config{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 || out[0].Name != "app" {
		t.Fatalf("expected only the app network, got %#v", out)
	}
	if out[0].Labels == nil {
		t.Error("expected labels to be retained by default")
	}

	// HideLabels=network: labels stripped.
	out, err = getNetworksInfo(context.Background(), src, &config.Config{HideLabels: []string{"network"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out[0].Labels != nil {
		t.Errorf("expected network labels to be hidden, got %#v", out[0].Labels)
	}
}

func TestGetInfo_PropagatesError(t *testing.T) {
	src := fakeSource{err: context.DeadlineExceeded}
	if _, err := getTasksInfo(context.Background(), src, &config.Config{}); err == nil {
		t.Error("expected error from getTasksInfo")
	}
	if _, err := getNodesInfo(context.Background(), src, &config.Config{}); err == nil {
		t.Error("expected error from getNodesInfo")
	}
}
