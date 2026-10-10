package docker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jtgasper3/swarm-visualizer/internal/config"
	"github.com/moby/moby/api/types/swarm"
)

// countingSource records how often the service list is read.
type countingSource struct {
	*fakeSource
	serviceReads int
}

func (c *countingSource) Services(ctx context.Context) ([]swarm.Service, error) {
	c.serviceReads++
	return c.fakeSource.Services(ctx)
}

func testService(id, hideEnvs string, env ...string) swarm.Service {
	labels := map[string]string{}
	if hideEnvs != "" {
		labels[hideEnvsLabel] = hideEnvs
	}
	return swarm.Service{ID: id, Spec: swarm.ServiceSpec{
		Annotations:  swarm.Annotations{Name: "name-" + id, Labels: labels},
		TaskTemplate: swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{Env: env}},
	}}
}

func testTask(id, serviceID string, env ...string) swarm.Task {
	return swarm.Task{ID: id, ServiceID: serviceID, DesiredState: swarm.TaskStateRunning,
		Status: swarm.TaskStatus{State: swarm.TaskStateRunning},
		Spec:   swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{Env: env}}}
}

func newTestInspector(src swarmSource) (*inspector, *Hub) {
	hub := newHub(&config.Config{}, nil)
	return newInspector(&config.Config{}, src, hub), hub
}

// publishedFrame returns the snapshot the inspector just published.
func publishedFrame(t *testing.T, h *Hub) string {
	t.Helper()
	select {
	case frame := <-h.broadcast:
		return string(frame)
	default:
		t.Fatal("expected a snapshot to be published")
		return ""
	}
}

func TestInspector_LoadPublishesAnEmptySwarm(t *testing.T) {
	in, hub := newTestInspector(&fakeSource{})
	in.load(context.Background())

	if frame := publishedFrame(t, hub); !strings.Contains(frame, `"tasks":null`) {
		t.Errorf("unexpected frame for an empty swarm: %s", frame)
	}
}

// The task poll runs more often than the service poll, so a task can show up
// before the service that says which of its variables to hide.
func TestInspector_NewServiceIsRedactedOnFirstSight(t *testing.T) {
	src := &fakeSource{
		services: []swarm.Service{testService("s1", "", "A=1")},
		tasks:    []swarm.Task{testTask("t1", "s1", "A=1")},
	}
	in, hub := newTestInspector(src)
	ctx := context.Background()
	in.load(ctx)
	publishedFrame(t, hub)

	src.services = append(src.services, testService("s2", "DB_PASSWORD", "DB_PASSWORD=hunter2"))
	src.tasks = append(src.tasks, testTask("t2", "s2", "DB_PASSWORD=hunter2"))

	// Only the task tick fires.
	if !in.refreshTasks(ctx) {
		t.Fatal("expected the task refresh to succeed")
	}
	in.publish()

	frame := publishedFrame(t, hub)
	if strings.Contains(frame, "hunter2") {
		t.Errorf("hidden variable published: %s", frame)
	}
	if got := strings.Count(frame, "DB_PASSWORD=(sanitized)"); got != 2 {
		t.Errorf("expected the service and its task to show the sanitized variable, found %d in %s", got, frame)
	}
	if !strings.Contains(frame, "name-s2") {
		t.Errorf("expected the new service to arrive with its task: %s", frame)
	}
}

func TestInspector_UpdateAddingAHiddenVariableIsRedactedOnFirstSight(t *testing.T) {
	src := &fakeSource{
		services: []swarm.Service{testService("s1", "", "A=1")},
		tasks:    []swarm.Task{testTask("t1", "s1", "A=1")},
	}
	in, hub := newTestInspector(src)
	ctx := context.Background()
	in.load(ctx)
	publishedFrame(t, hub)

	// One update adds a variable and lists it in hide-envs; swarm starts a
	// task from the new spec.
	src.services = []swarm.Service{testService("s1", "TOKEN", "A=1", "TOKEN=hunter2")}
	src.tasks = []swarm.Task{testTask("t1", "s1", "A=1"), testTask("t2", "s1", "A=1", "TOKEN=hunter2")}

	if !in.refreshTasks(ctx) {
		t.Fatal("expected the task refresh to succeed")
	}
	in.publish()

	if frame := publishedFrame(t, hub); strings.Contains(frame, "hunter2") {
		t.Errorf("hidden variable published: %s", frame)
	}
}

// An update that removes a variable together with its hide-envs entry leaves
// tasks from the old spec running with the value.
func TestInspector_OldTasksStayRedactedAfterTheLabelIsRemoved(t *testing.T) {
	previous := testService("s1", "TOKEN", "TOKEN=hunter2").Spec
	current := testService("s1", "", "A=1")
	current.PreviousSpec = &previous

	src := &fakeSource{
		services: []swarm.Service{current},
		tasks:    []swarm.Task{testTask("old", "s1", "TOKEN=hunter2"), testTask("new", "s1", "A=1")},
	}
	in, hub := newTestInspector(src)
	in.load(context.Background())

	frame := publishedFrame(t, hub)
	if strings.Contains(frame, "hunter2") {
		t.Errorf("hidden variable published: %s", frame)
	}
	if strings.Contains(frame, "PreviousSpec") {
		t.Errorf("previous spec published: %s", frame)
	}
}

func TestInspector_WithholdsNewTaskUntilServicesCanBeRead(t *testing.T) {
	src := &fakeSource{
		services: []swarm.Service{testService("s1", "", "A=1")},
		tasks:    []swarm.Task{testTask("t1", "s1", "A=1")},
	}
	in, hub := newTestInspector(src)
	ctx := context.Background()
	in.load(ctx)
	publishedFrame(t, hub)

	src.services = append(src.services, testService("s2", "DB_PASSWORD", "DB_PASSWORD=hunter2"))
	src.tasks = append(src.tasks, testTask("t2", "s2", "DB_PASSWORD=hunter2"))
	src.servicesErr = errors.New("service list unavailable")

	if in.refreshTasks(ctx) {
		t.Fatal("expected the task refresh to fail while the services cannot be read")
	}
	if ids := taskIDs(in.tasks); ids["t2"] || !ids["t1"] {
		t.Errorf("expected the previous tasks to be kept as they were, got %v", ids)
	}

	src.servicesErr = nil
	if !in.refreshTasks(ctx) {
		t.Fatal("expected the task refresh to succeed once the services can be read")
	}
	in.publish()
	frame := publishedFrame(t, hub)
	if strings.Contains(frame, "hunter2") {
		t.Errorf("hidden variable published: %s", frame)
	}
	if !taskIDs(in.tasks)["t2"] {
		t.Error("expected the new task to be published after the retry")
	}
}

func TestInspector_ReadsServicesOnlyForNewTasks(t *testing.T) {
	src := &countingSource{fakeSource: &fakeSource{
		services: []swarm.Service{testService("s1", "", "A=1")},
		tasks:    []swarm.Task{testTask("t1", "s1", "A=1")},
	}}
	in, _ := newTestInspector(src)
	ctx := context.Background()

	in.refreshTasks(ctx)
	if src.serviceReads != 1 {
		t.Fatalf("expected the first poll to read the services once, got %d", src.serviceReads)
	}

	in.refreshTasks(ctx)
	in.refreshTasks(ctx)
	if src.serviceReads != 1 {
		t.Errorf("expected no further reads while the tasks are unchanged, got %d", src.serviceReads)
	}

	// A task going away is not a reason to re-read the services.
	src.tasks = nil
	in.refreshTasks(ctx)
	if src.serviceReads != 1 {
		t.Errorf("expected no read for a removed task, got %d", src.serviceReads)
	}

	src.tasks = []swarm.Task{testTask("t2", "s1", "A=1")}
	in.refreshTasks(ctx)
	if src.serviceReads != 2 {
		t.Errorf("expected one more read for the new task, got %d", src.serviceReads)
	}
}

func TestInspector_UnchangedSwarmIsPublishedOnce(t *testing.T) {
	stoppedTaskCache = make(map[string]cachedTask)
	t.Cleanup(func() { stoppedTaskCache = make(map[string]cachedTask) })

	src := &fakeSource{services: []swarm.Service{testService("s1", "", "A=1")}}
	for _, id := range []string{"f", "b", "e", "a", "d", "c"} {
		failed := testTask(id, "s1", "A=1")
		failed.Slot = len(src.tasks) + 1
		failed.DesiredState = swarm.TaskStateShutdown
		failed.Status.State = swarm.TaskStateFailed
		failed.UpdatedAt = time.Now()
		src.tasks = append(src.tasks, failed)
	}
	in, hub := newTestInspector(src)
	ctx := context.Background()
	in.load(ctx)
	publishedFrame(t, hub)

	for i := 0; i < 50; i++ {
		in.refreshTasks(ctx)
		in.publish()
		select {
		case frame := <-hub.broadcast:
			t.Fatalf("poll %d republished an unchanged swarm: %s", i, frame)
		default:
		}
	}
}

func TestRunStep_ContainsPanic(t *testing.T) {
	runStep(func() {
		var cs *swarm.ContainerSpec
		_ = cs.Env // nil dereference, as an unexpected API shape would cause
	})

	ran := false
	runStep(func() { ran = true })
	if !ran {
		t.Error("expected a later step to run after one panicked")
	}
}
