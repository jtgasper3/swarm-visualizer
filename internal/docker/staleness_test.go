package docker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jtgasper3/swarm-visualizer/internal/config"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/swarm"
)

// hangingSource never answers; it returns only when its context ends.
type hangingSource struct{}

func (hangingSource) Nodes(ctx context.Context) ([]swarm.Node, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (hangingSource) Services(ctx context.Context) ([]swarm.Service, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (hangingSource) Tasks(ctx context.Context) ([]swarm.Task, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (hangingSource) Networks(ctx context.Context) ([]network.Summary, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// A daemon that accepts the connection and then says nothing must not hold the
// poll loop for good.
func TestInspector_CutsOffCallsThatHang(t *testing.T) {
	const timeout = 50 * time.Millisecond

	polls := map[string]func(*inspector) bool{
		"tasks": func(in *inspector) bool { return in.refreshTasks(context.Background()) },
		// Three calls, but one deadline between them.
		"nodes, services and networks": func(in *inspector) bool { return in.refreshStructural(context.Background()) },
	}
	for name, poll := range polls {
		t.Run(name, func(t *testing.T) {
			in, _ := newTestInspector(hangingSource{})
			in.callTimeout = timeout
			var logged []string
			logf := func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
			in.taskErrors.logf, in.structuralErrors.logf = logf, logf

			started := time.Now()
			done := make(chan bool, 1)
			go func() { done <- poll(in) }()
			select {
			case ok := <-done:
				if ok {
					t.Error("a poll that timed out reported success")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("the poll was not cut off")
			}
			if took := time.Since(started); took > 3*timeout-timeout/2 {
				t.Errorf("the poll took %s; with one deadline it should take about %s", took, timeout)
			}
			if len(logged) != 1 || !strings.Contains(logged[0], "deadline exceeded") {
				t.Errorf("expected one log line naming the timeout, got %q", logged)
			}
		})
	}
}

func TestPollErrors_LogsOnceThenAtIntervals(t *testing.T) {
	var lines []string
	p := pollErrors{what: "tasks", logf: func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	}}
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	boom := errors.New("connection refused")

	p.succeeded(start)
	if len(lines) != 0 {
		t.Fatalf("a success with nothing to recover from logged %q", lines)
	}

	// One failure a second for a minute and a half.
	for i := 0; i < 90; i++ {
		p.failed(start.Add(time.Duration(i)*time.Second), boom)
	}
	if len(lines) != 3 {
		t.Fatalf("90 failures produced %d log lines, want 3 (the first, then one per %s): %q", len(lines), pollErrorLogInterval, lines)
	}
	if !strings.Contains(lines[0], "Error fetching tasks: connection refused") {
		t.Errorf("first line = %q", lines[0])
	}
	if !strings.Contains(lines[1], "after 30s") || !strings.Contains(lines[1], "29 errors not logged") {
		t.Errorf("reminder = %q, want the duration and the number of errors left out", lines[1])
	}

	p.succeeded(start.Add(95 * time.Second))
	if len(lines) != 4 || !strings.Contains(lines[3], "works again after 1m35s") {
		t.Errorf("recovery line = %q", lines[len(lines)-1])
	}

	// Errors from several calls in one poll stay on one line.
	p.failed(start.Add(96*time.Second), errors.Join(errors.New("nodes: refused"), errors.New("services: refused")))
	if got := lines[len(lines)-1]; strings.Contains(got, "\n") || !strings.Contains(got, "nodes: refused; services: refused") {
		t.Errorf("joined errors were not logged on one line: %q", got)
	}
	p.succeeded(start.Add(97 * time.Second))
	lines = lines[:4]

	// The next failure is a new run and is logged straight away.
	p.failed(start.Add(100*time.Second), boom)
	if len(lines) != 5 || !strings.Contains(lines[4], "Error fetching tasks") {
		t.Errorf("a failure after a recovery was not logged as a new one: %q", lines)
	}
}

// staleFixture is an inspector with a clock the test moves.
type staleFixture struct {
	in  *inspector
	hub *Hub
	src *fakeSource
	now time.Time
}

func newStaleFixture() *staleFixture {
	f := &staleFixture{
		src: &fakeSource{
			nodes:    []swarm.Node{{ID: "n1"}},
			services: []swarm.Service{testService("s1", "", "A=1")},
			tasks:    []swarm.Task{testTask("t1", "s1", "A=1")},
		},
		now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
	}
	f.in, f.hub = newTestInspector(f.src)
	f.in.now = func() time.Time { return f.now }
	quiet := func(string, ...any) {}
	f.in.taskErrors.logf, f.in.structuralErrors.logf = quiet, quiet
	return f
}

// tick moves the clock and then does what the loop does when both of its
// tickers fire.
func (f *staleFixture) tick(d time.Duration) {
	f.now = f.now.Add(d)
	ctx := context.Background()
	f.in.taskTick(ctx)
	f.in.pollStructural(ctx)
}

// watch connects a client, so that the hub is not idle.
func (f *staleFixture) watch() *wsClient {
	c := &wsClient{send: make(chan []byte, 1)}
	f.hub.register(c)
	return c
}

func (f *staleFixture) frame() (string, bool) {
	select {
	case frame := <-f.hub.broadcast:
		return string(frame), true
	default:
		return "", false
	}
}

// A snapshot is only sent when it changes. If the swarm cannot be read, nothing
// changes, and a dashboard would go on showing the last view as though it were
// current.
func TestInspector_SaysWhenTheSnapshotIsStale(t *testing.T) {
	// "nothing" is a complete outage: no poll succeeds, so the only publish
	// that can say so is the one after a failed poll.
	for _, failing := range []string{"tasks", "services", "nothing"} {
		t.Run(failing+" cannot be read", func(t *testing.T) {
			f := newStaleFixture()
			f.watch()
			f.in.load(context.Background())
			first, ok := f.frame()
			if !ok || strings.Contains(first, `"stale"`) {
				t.Fatalf("expected a fresh first snapshot, got %q", first)
			}
			lastGood := f.now

			switch failing {
			case "tasks":
				f.src.tasksErr = errors.New("unavailable")
			case "services":
				f.src.servicesErr = errors.New("unavailable")
			default:
				f.src.err = errors.New("unavailable")
			}

			// Short of the threshold nothing is said.
			f.tick(staleAfter - time.Second)
			if frame, ok := f.frame(); ok {
				t.Fatalf("a snapshot was sent before the data was stale: %s", frame)
			}

			f.tick(2 * time.Second)
			stale, ok := f.frame()
			if !ok {
				t.Fatal("expected a snapshot saying the data is stale")
			}
			if !strings.Contains(stale, `"stale":true`) {
				t.Errorf("snapshot does not say it is stale: %s", stale)
			}
			if want := `"lastUpdated":"` + lastGood.Format(time.RFC3339) + `"`; !strings.Contains(stale, want) {
				t.Errorf("snapshot does not carry %s: %s", want, stale)
			}
			if !strings.Contains(stale, "name-s1") {
				t.Errorf("a stale snapshot should still carry the last known data: %s", stale)
			}

			// It is said once, not on every failed poll.
			for i := 0; i < 20; i++ {
				f.tick(time.Second)
				if frame, ok := f.frame(); ok {
					t.Fatalf("poll %d resent the stale snapshot: %s", i, frame)
				}
			}

			f.src.tasksErr, f.src.servicesErr, f.src.err = nil, nil, nil
			f.tick(time.Second)
			recovered, ok := f.frame()
			if !ok {
				t.Fatal("expected a snapshot once the swarm could be read again")
			}
			if strings.Contains(recovered, `"stale"`) || strings.Contains(recovered, `"lastUpdated"`) {
				t.Errorf("recovered snapshot still says it is stale: %s", recovered)
			}
		})
	}
}

// Either ticker can be the one that fires once the threshold has passed, so
// each poll has to be able to say the snapshot is stale by itself.
func TestInspector_EitherPollReportsAnOutage(t *testing.T) {
	polls := map[string]func(*staleFixture){
		"task tick":       func(f *staleFixture) { f.in.taskTick(context.Background()) },
		"structural tick": func(f *staleFixture) { f.in.pollStructural(context.Background()) },
	}
	for name, poll := range polls {
		t.Run(name, func(t *testing.T) {
			f := newStaleFixture()
			f.watch()
			f.in.load(context.Background())
			if _, ok := f.frame(); !ok {
				t.Fatal("expected a first snapshot")
			}

			f.src.err = errors.New("unavailable")
			f.now = f.now.Add(staleAfter + time.Second)
			poll(f)

			if frame, ok := f.frame(); !ok || !strings.Contains(frame, `"stale":true`) {
				t.Errorf("the %s did not report the outage: %q", name, frame)
			}
		})
	}
}

func TestInspector_PollsSlowlyWhileNobodyIsConnected(t *testing.T) {
	f := newStaleFixture()
	src := &countingSource{fakeSource: f.src}
	f.in.src = src
	f.in.load(context.Background())
	if _, ok := f.frame(); !ok {
		t.Fatal("expected a first snapshot")
	}
	if src.taskReads != 1 {
		t.Fatalf("the first load read the tasks %d times, want once", src.taskReads)
	}

	// Ten seconds of ticks with nobody connected.
	for i := 0; i < 10; i++ {
		f.tick(time.Second)
	}
	if got := src.taskReads - 1; got != 2 {
		t.Errorf("10 idle seconds read the tasks %d times, want 2 (one per %s)", got, idleTaskPollInterval)
	}

	// With a client connected, every tick reads them.
	f.watch()
	before := src.taskReads
	for i := 0; i < 10; i++ {
		f.tick(time.Second)
	}
	if got := src.taskReads - before; got != 10 {
		t.Errorf("10 seconds with a client connected read the tasks %d times, want 10", got)
	}

	// Reading them less often must not make the snapshot look stale.
	if frame, ok := f.frame(); ok {
		t.Errorf("an unchanged swarm was republished: %s", frame)
	}
}

// Close has to wait for the closing frames to be written, or a shutdown that
// follows straight on would drop them with the connections.
func TestHub_CloseWaitsForWriters(t *testing.T) {
	// A writer that never finishes: Close gives up only when its context does.
	stuck := newHub(&config.Config{}, nil)
	stuck.register(&wsClient{send: make(chan []byte, 1)})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	stuck.Close(ctx)
	if waited := time.Since(started); waited < 80*time.Millisecond {
		t.Errorf("Close returned after %s with a writer still running", waited)
	}

	// A writer that takes a moment: Close returns when it is done.
	h := newHub(&config.Config{}, nil)
	c := &wsClient{send: make(chan []byte, 1)}
	h.register(c)
	finished := make(chan struct{})
	go func() {
		for range c.send { // drained until Close closes it
		}
		time.Sleep(50 * time.Millisecond)
		close(finished)
		h.writers.Done()
	}()
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h.Close(ctx)
	select {
	case <-finished:
	default:
		t.Error("Close returned before the writer had finished")
	}
	if ctx.Err() != nil {
		t.Error("Close waited for its context although the writer had finished")
	}
	if h.register(&wsClient{send: make(chan []byte, 1)}) {
		t.Error("a closed hub accepted a client")
	}
}

// The first client to arrive is what ends the slow polling, so the inspector
// has to hear about it.
func TestHub_SignalsTheFirstArrival(t *testing.T) {
	h := newHub(&config.Config{}, nil)
	if !h.Idle() {
		t.Fatal("a new hub should be idle")
	}

	first := &wsClient{send: make(chan []byte, 1)}
	h.register(first)
	select {
	case <-h.arrivals:
	default:
		t.Fatal("expected a signal when the first client connected")
	}
	if h.Idle() {
		t.Error("a hub with a client is not idle")
	}

	h.register(&wsClient{send: make(chan []byte, 1)})
	select {
	case <-h.arrivals:
		t.Error("a second client is not an arrival from idle")
	default:
	}

	// The signal never blocks registration, even if nobody is listening.
	h.unregister(first)
	other := newHub(&config.Config{}, nil)
	for i := 0; i < 3; i++ {
		c := &wsClient{send: make(chan []byte, 1)}
		other.register(c)
		other.unregister(c)
	}
}

// Each redaction path is applied when its group is fetched. The result has to
// be the same as when they were all applied on every publish.
func TestInspector_AppliesRedactionPathsAtFetch(t *testing.T) {
	f := newStaleFixture()
	f.src.networks = []network.Summary{{Network: network.Network{Name: "app", Labels: map[string]string{"k": "net-secret"}}}}
	f.src.nodes = []swarm.Node{{ID: "n1", Description: swarm.NodeDescription{Hostname: "host-secret"}}}
	f.in.cfg = &config.Config{ClusterName: "cluster-secret", SensitiveDataPaths: []string{
		"tasks.*.Spec.ContainerSpec.Env",
		"services.*.Spec.TaskTemplate.ContainerSpec.Env",
		"networks.*.Labels",
		"Nodes.*.Description.Hostname", // by Go name, which paths may also use
		"clusterName",
	}}
	in := newInspector(f.in.cfg, f.src, f.hub)

	if got, want := strings.Join(in.taskPaths, ","), "tasks.*.Spec.ContainerSpec.Env"; got != want {
		t.Errorf("task paths = %q, want %q", got, want)
	}
	if len(in.structuralPaths) != 3 || len(in.otherPaths) != 1 {
		t.Errorf("structural paths = %q, other paths = %q", in.structuralPaths, in.otherPaths)
	}

	in.load(context.Background())
	frame, ok := f.frame()
	if !ok {
		t.Fatal("expected a snapshot")
	}
	for _, secret := range []string{"A=1", "net-secret", "host-secret", "cluster-secret"} {
		if strings.Contains(frame, secret) {
			t.Errorf("%q survived redaction: %s", secret, frame)
		}
	}
	if !strings.Contains(frame, "name-s1") {
		t.Errorf("unrelated fields were cleared: %s", frame)
	}

	// A later poll brings fresh, unredacted data; it has to be cleared again.
	f.src.tasks = []swarm.Task{testTask("t1", "s1", "A=1"), testTask("t2", "s1", "B=2")}
	in.refreshTasks(context.Background())
	in.publish()
	if frame, _ := f.frame(); strings.Contains(frame, "B=2") || !strings.Contains(frame, `"t2"`) {
		t.Errorf("a task fetched later was not redacted, or is missing: %s", frame)
	}
}

// A path can name a whole group. Clearing it has to empty the group that is
// published, not a copy of it.
func TestInspector_ClearsWholeGroups(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"tasks", `"tasks":null`},
		{"Tasks", `"tasks":null`},
		{"services", `"services":null`},
		{"nodes", `"nodes":null`},
		{"networks", `"networks":null`},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			f := newStaleFixture()
			f.src.networks = []network.Summary{{Network: network.Network{Name: "app"}}}
			in := newInspector(&config.Config{SensitiveDataPaths: []string{tc.path}}, f.src, f.hub)
			ctx := context.Background()
			in.load(ctx)

			frame, ok := f.frame()
			if !ok {
				t.Fatal("expected a snapshot")
			}
			if !strings.Contains(frame, tc.want) {
				t.Errorf("path %q did not clear its group: %s", tc.path, frame)
			}

			// And it stays cleared on later polls of either kind.
			in.refreshStructural(ctx)
			in.refreshTasks(ctx)
			in.publish()
			if later, ok := f.frame(); ok && !strings.Contains(later, tc.want) {
				t.Errorf("path %q stopped clearing its group on a later poll: %s", tc.path, later)
			}
		})
	}
}

func TestInspector_RunStopsWhenTheContextEnds(t *testing.T) {
	stoppedTaskCache = make(map[string]cachedTask)
	t.Cleanup(func() { stoppedTaskCache = make(map[string]cachedTask) })

	f := newStaleFixture()
	f.in.now = time.Now
	go f.hub.runBroadcasts()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		f.in.run(ctx)
		close(done)
	}()

	if !waitFor(t, f.hub.Ready, 2*time.Second) {
		t.Fatal("the inspector did not publish its first snapshot")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("run did not return after its context ended")
	}
}
