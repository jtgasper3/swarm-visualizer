package main

// These tests put the whole thing together: a stand-in for the Docker API
// serves a swarm from testdata, the real server reads it, and a real browser
// renders the page. They are what notices a template that throws on a shape of
// data nobody thought of, which no test of the server or of the frontend's
// helpers can.
//
// They need Chrome or Chromium; see findBrowser. To reproduce something seen
// on a real swarm, save what its daemon answers for /nodes, /services, /tasks
// and /networks into a directory under testdata and point a test at it.

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jtgasper3/swarm-visualizer/internal/config"
	"github.com/moby/moby/client"
)

var apiVersionPrefix = regexp.MustCompile(`^/v[0-9.]+`)

// fakeDocker answers the Docker API calls the server makes with the files in
// dir: nodes.json, services.json, tasks.json and networks.json, each holding
// what the daemon would send. "$NOW" in a file becomes the time the fake was
// started, for what the server only shows while it is recent: a task that
// failed stays on the dashboard for half a minute.
func fakeDocker(t *testing.T, dir string) *httptest.Server {
	t.Helper()

	now := []byte(time.Now().UTC().Format(time.RFC3339Nano))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", client.MaxAPIVersion)
		w.Header().Set("Ostype", "linux")

		switch name := apiVersionPrefix.ReplaceAllString(r.URL.Path, ""); name {
		case "/_ping":
			_, _ = w.Write([]byte("OK"))
		case "/nodes", "/services", "/tasks", "/networks":
			body, err := os.ReadFile(filepath.Join(dir, name[1:]+".json"))
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(bytes.ReplaceAll(body, []byte("$NOW"), now))
		default:
			t.Errorf("the server asked the Docker API for %s %s, which the fixture does not provide", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// startDashboard runs the whole server against the swarm described in dir and
// returns its address, and a function that shuts it down the way main does.
// The server is shut down when the test ends in any case.
func startDashboard(t *testing.T, dir string) (url string, stop func()) {
	t.Helper()

	// Whatever the developer's shell has set must not reach the server. This
	// is every setting it reads; one of them, a secret file that is not
	// there, would end the whole test run.
	for _, name := range []string{
		"DOCKER_API_VERSION", "DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY",
		"CONTEXT_ROOT", "LISTENER_PORT", "STATIC_DIR", "MAX_WS_CONNECTIONS", "TRUSTED_PROXIES",
		"HIDE_ALL_CONFIGS", "HIDE_ALL_ENVS", "HIDE_ALL_MOUNTS", "HIDE_ALL_SECRETS", "HIDE_LABELS", "SENSITIVE_DATA_PATHS",
		"ENABLE_AUTHN", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET_FILE", "OIDC_REDIRECT_URL", "OIDC_SCOPES",
		"OIDC_WELL_KNOWN_URL", "OIDC_AUTH_URL", "OIDC_TOKEN_URL", "OIDC_USERNAME_CLAIM", "OIDC_SESSION_MAX_AGE",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("DOCKER_HOST", "tcp://"+fakeDocker(t, dir).Listener.Addr().String())
	t.Setenv("CLUSTER_NAME", "Fixture Swarm")

	ctx, cancel := context.WithCancel(context.Background())
	handler, hub := newHandler(ctx, config.LoadConfig())
	srv := httptest.NewServer(handler)
	stop = sync.OnceFunc(func() {
		cancel()
		closing, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		hub.Close(closing)
		srv.Close()
	})
	t.Cleanup(stop)
	return srv.URL, stop
}

// openDashboard starts a browser and a server for the swarm in
// testdata/<fixture>, and returns the page once it shows cards.
func openDashboard(t *testing.T, fixture string, cards int) *page {
	t.Helper()
	if testing.Short() {
		t.Skip("needs a browser")
	}

	p := startBrowser(t)
	url, _ := startDashboard(t, filepath.Join("testdata", fixture))
	p.open(t, url+"/")
	p.waitFor(t, fmt.Sprintf(`document.querySelectorAll('.server-card').length === %d`, cards))
	return p
}

// card is what one card on the dashboard shows: a node's, or the one for
// tasks that are on no node.
type card struct {
	Name      string   `json:"name"`
	Chips     []string `json:"chips"`
	Resources []string `json:"resources"`
	Tasks     []string `json:"tasks"`
}

// taskItems selects the tasks a card lists. Each task is a card with a list of
// its own inside, so the outer list is picked out by its label.
const taskItems = `.v-list[aria-label^="Services on"] > .v-list-item, .v-list[aria-label="Unassigned tasks"] > .v-list-item`

// readCards describes every card on the page. A task reads as its service, the
// image it runs and its state.
const readCards = `[...document.querySelectorAll('.server-card')].map(card => {
	const clean = text => (text ?? '').replace(/\s+/g, ' ').trim();
	return {
		name: card.querySelector(':scope > .v-list-item').getAttribute('aria-label'),
		chips: [...card.querySelectorAll('.v-card-text > .v-chip')].map(chip => clean(chip.innerText)),
		resources: [...card.querySelectorAll('table tbody tr')].map(row => clean(row.innerText)),
		tasks: [...card.querySelectorAll('` + taskItems + `')].map(task => clean([
			task.getAttribute('aria-label'),
			task.querySelector(':scope .v-card-text span[title]')?.getAttribute('title'),
			clean(task.querySelector('.v-card-subtitle')?.innerText).replace(/ for [0-9dhm< ]+$/, ''),
		].join(' | '))),
	};
})`

// assertNothingWentWrong fails if the page reported an error: one written to
// the console, which is where the page's own error handler sends what it
// catches, an uncaught one, or one from the browser, such as a file that did
// not load or a request the content security policy refused.
func assertNothingWentWrong(t *testing.T, p *page) {
	t.Helper()
	for _, problem := range p.reported() {
		t.Errorf("the page reported: %s", problem)
	}
	if banner := p.text(t, ".v-alert"); banner != "" {
		t.Errorf("the page shows an error banner: %s", banner)
	}
}

func TestDashboardInABrowser(t *testing.T) {
	p := openDashboard(t, "swarm", 4)

	t.Run("names itself and the cluster", func(t *testing.T) {
		var title string
		p.eval(t, `document.title`, &title)
		if title != "Swarm Visualizer" {
			t.Errorf("title = %q", title)
		}
		if bar := p.text(t, ".v-app-bar"); !strings.Contains(bar, "Fixture Swarm") {
			t.Errorf("the app bar reads %q, without the cluster name", bar)
		}
	})

	t.Run("cards", func(t *testing.T) {
		want := []card{
			{
				// A task the swarm cannot place is on no node's card.
				Name:      "Unassigned tasks",
				Chips:     []string{},
				Resources: []string{},
				Tasks:     []string{"shop_worker | registry.example.com/shop/worker:0.7 | replicated pending"},
			},
			{
				Name:  "mgr-01",
				Chips: []string{"manager", "leader", "x86_64"},
				// The totals are those of the tasks on the node: one shop_web
				// task on the new spec, and the agent's 64 MB limit.
				Resources: []string{"vCPUs 8 0.5 1", "Memory 16.00 GB 256.00 MB 576.00 MB"},
				Tasks: []string{
					"agent | example/agent:3 | global running",
					"shop_web | registry.example.com/shop/web:2.0 | replicated running",
				},
			},
			{
				// A manager that is not the leader. The task here whose
				// service has been removed is not listed.
				Name:      "mgr-02",
				Chips:     []string{"manager", "x86_64"},
				Resources: []string{"vCPUs 8 0 0", "Memory 16.00 GB 0.00 B 64.00 MB"},
				Tasks:     []string{"agent | example/agent:3 | global running"},
			},
			{
				Name:  "wkr-01",
				Chips: []string{"worker", "aarch64"},
				// shop_web's other task is still on the old image and the old,
				// smaller reservation. The failed shop_api task reserved a
				// whole CPU and holds nothing now.
				Resources: []string{"vCPUs 4 0.25 0.5", "Memory 8.00 GB 128.00 MB 320.00 MB"},
				Tasks: []string{
					"agent | example/agent:3 | global starting",
					// A plugin service runs no container, so has no image.
					"logshipper | | global running",
					// No resources block, and the first network it is attached
					// to is not in the list.
					"shop_api | registry.example.com/shop/api:1.4 | replicated running",
					"shop_api | registry.example.com/shop/api:1.4 | replicated failed",
					"shop_web | registry.example.com/shop/web:1.9 | replicated running",
				},
			},
		}

		var got []card
		p.eval(t, readCards, &got)
		if len(got) != len(want) {
			t.Fatalf("the page shows %d cards, want %d: %+v", len(got), len(want), got)
		}
		for i := range want {
			if !reflect.DeepEqual(got[i], want[i]) {
				t.Errorf("card %d\n got: %+v\nwant: %+v", i, got[i], want[i])
			}
		}
	})

	// The failed task is only on the dashboard for half a minute after the
	// server first sees it, so what depends on it comes first.
	t.Run("the stopped filter leaves only the failed task", func(t *testing.T) {
		p.eval(t, `[...document.querySelectorAll('.v-navigation-drawer button')].find(button => button.innerText.trim() === 'STOPPED').click()`, nil)
		p.waitFor(t, `document.querySelectorAll('`+taskItems+`').length === 1`)

		var got []card
		p.eval(t, readCards, &got)
		var tasks []string
		for _, each := range got {
			for _, task := range each.Tasks {
				tasks = append(tasks, each.Name+": "+task)
			}
		}
		want := []string{"wkr-01: shop_api | registry.example.com/shop/api:1.4 | replicated failed"}
		if !reflect.DeepEqual(tasks, want) {
			t.Errorf("tasks shown: %q, want %q", tasks, want)
		}

		p.eval(t, `[...document.querySelectorAll('button')].find(button => button.innerText.trim() === 'RESET').click()`, nil)
		p.waitFor(t, `document.querySelectorAll('`+taskItems+`').length === 9`)
	})

	t.Run("each service has a border of its own", func(t *testing.T) {
		var borders map[string][]string
		p.eval(t, `(() => {
			const borders = {};
			for (const task of document.querySelectorAll('`+taskItems+`')) {
				const style = getComputedStyle(task.querySelector('.v-card'));
				(borders[task.getAttribute('aria-label')] ??= []).push(style.borderTopColor + ' ' + style.borderTopStyle);
			}
			return borders;
		})()`, &borders)

		seen := map[string]string{}
		for service, each := range borders {
			for _, border := range each {
				if border != each[0] {
					t.Errorf("%s has tasks with different borders: %v", service, each)
				}
			}
			if other, taken := seen[each[0]]; taken {
				t.Errorf("%s and %s share the border %s", service, other, each[0])
			}
			seen[each[0]] = service
		}
		if len(borders) != 5 {
			t.Errorf("found borders for %d services, want 5: %v", len(borders), borders)
		}
	})

	t.Run("the drawer counts running against wanted", func(t *testing.T) {
		p.eval(t, `document.querySelector('[aria-label="Expand shop"]').click()`, nil)
		p.waitFor(t, `document.querySelector('.v-navigation-drawer').innerText.includes('shop_worker')`)

		drawer := p.text(t, ".v-navigation-drawer")
		for _, want := range []string{
			"Nodes 3/3", "Services 5/5",
			"agent 2/3",       // one of its three tasks is still starting
			"logshipper 1/1",  //
			"shop_api 1/1",    // the failed task has been replaced
			"shop_web 2/2",    //
			"shop_worker 0/1", // its only task cannot be placed
			"shop_frontend", "(none)",
		} {
			if !strings.Contains(drawer, want) {
				t.Errorf("the drawer does not show %q; it reads: %s", want, drawer)
			}
		}
		if strings.Contains(drawer, "ingress") {
			t.Errorf("the drawer lists the ingress network: %s", drawer)
		}
	})

	t.Run("details", func(t *testing.T) {
		tests := []struct {
			target string
			want   []string
		}{
			// First, while the failed task is still on the dashboard.
			{"task/task-api-1", []string{"shop_api.1 Task Details", "Status failed (desired: shutdown)", "task: non-zero exit (137)"}},

			{"node/node-mgr-01", []string{
				"mgr-01 Node Details", "Role manager (leader)", "Manager reachable — 10.0.0.11:2377",
				"Platform linux on x86_64", "Resources 8 vCPUs / 16.00 GB", "Labels zone=a", "Engine Version 29.8.0",
			}},
			{"node/node-mgr-02", []string{"mgr-02 Node Details", "Role manager Availability"}},
			{"node/node-wkr-01", []string{"wkr-01 Node Details", "Role worker Availability", "Resources 4 vCPUs / 8.00 GB"}},

			{"service/svc-web", []string{
				"shop_web Service Details", "Published Ports tcp: 80 → 8080 (ingress)",
				"Image registry.example.com/shop/web:2.0 ", "MODE=production LISTEN=:8080",
				"Networks shop_frontend (net-frontend): web", "Mode Replicated (2)",
				"Reservations: 0.5 vCPUs / 256.00 MB", "Limits: 1 vCPUs / 512.00 MB",
				"Restart Policy Condition: any", "Parallelism: 1 Delay: 10s Failure Action: pause Order: start-first",
			}},
			{"service/svc-api", []string{
				"shop_api Service Details",
				"Placement Constraints node.role == worker node.labels.zone == a",
				// A network the server did not list is shown by its ID.
				"Networks net-host-not-in-the-list (net-host-not-in-the-list) shop_frontend (net-frontend)",
				"Reservations: 0 vCPUs / 0.00 B",
			}},
			{"service/svc-agent", []string{"agent Service Details", "Image example/agent:3", "Mode Global", "Limits: 0 vCPUs / 64.00 MB"}},
			{"service/svc-worker", []string{"shop_worker Service Details", "Reservations: 0 vCPUs / 64.00 GB"}},
			{"service/svc-plugin", []string{"logshipper Service Details", "Mode Global"}},

			// Each task shows its own spec, not its service's.
			{"task/task-web-1", []string{
				"shop_web.1 Task Details", "Node ID node-mgr-01", "Status running",
				"Image registry.example.com/shop/web:2.0@sha256:2222", "Reservations: 0.5 vCPUs / 256.00 MB",
			}},
			{"task/task-web-2", []string{
				"shop_web.2 Task Details", "Node ID node-wkr-01",
				"Image registry.example.com/shop/web:1.9@sha256:1111", "Reservations: 0.25 vCPUs / 128.00 MB",
			}},
			{"task/task-api-2", []string{"shop_api.1 Task Details", "Status running", "Container ID c0ffee0003"}},
			{"task/task-agent-wkr-01", []string{"Task Details", "Status starting (desired: running)"}},
			{"task/task-worker-1", []string{
				"shop_worker.1 Task Details", "Status pending (desired: running)",
				"no suitable node (insufficient resources on 3 nodes)",
			}},
			{"task/task-plugin-wkr-01", []string{"logshipper", "Task Details", "Task: task-plugin-wkr-01"}},
			// Its service is gone; the task can still be looked at.
			{"task/task-orphan", []string{"svc-removed.1 Task Details", "Image example/removed:1"}},
		}

		const dialog = `document.querySelector('.v-overlay--active .v-card')`
		for _, tc := range tests {
			opened := t.Run(tc.target, func(t *testing.T) {
				// Wait for the ID, which only these details show. Two tasks of
				// one service have the same title, and the details of the last
				// target are still up until the page has caught up.
				kind, id, _ := strings.Cut(tc.target, "/")
				shown := map[string]string{"node": "ID: ", "service": "Service: ", "task": "Task: "}[kind] + id + " "
				p.eval(t, fmt.Sprintf(`location.hash = %q`, "#/"+tc.target), nil)
				p.waitFor(t, fmt.Sprintf(`(%s?.innerText.replace(/\s+/g, ' ') + ' ').includes(%q)`, dialog, shown))

				text := p.text(t, ".v-overlay--active .v-card")
				for _, want := range tc.want {
					if !strings.Contains(text, want) {
						t.Errorf("the details do not show %q; they read: %s", want, text)
					}
				}
				if strings.Contains(text, "undefined") || strings.Contains(text, "NaN") || strings.Contains(text, "[object") {
					t.Errorf("the details show a value that was not there to format: %s", text)
				}
			})
			if !opened && p.text(t, ".v-overlay--active .v-card") == "" {
				// Waiting out the same failure for every target helps nobody.
				t.Fatal("no details open at all; not trying the rest")
			}
		}

		t.Run("close", func(t *testing.T) {
			p.eval(t, `document.querySelector('.v-overlay--active [aria-label="Close details"]').click()`, nil)
			p.waitFor(t, `!`+dialog+` && location.hash === ''`)
		})

		t.Run("nothing opens for something that is not there", func(t *testing.T) {
			p.eval(t, `location.hash = '#/service/no-such-service'`, nil)
			p.eval(t, `new Promise(done => setTimeout(done, 300))`, nil)
			if text := p.text(t, ".v-overlay--active .v-card"); text != "" {
				t.Errorf("a dialog opened: %s", text)
			}
			p.eval(t, `history.replaceState(null, '', location.pathname)`, nil)
		})
	})

	assertNothingWentWrong(t, p)
}

// A swarm with nothing deployed sends no tasks at all, not an empty list.
func TestDashboardInABrowser_EmptySwarm(t *testing.T) {
	p := openDashboard(t, "empty", 1)

	var got []card
	p.eval(t, readCards, &got)
	want := []card{{
		Name:      "solo-01",
		Chips:     []string{"manager", "leader", "aarch64"},
		Resources: []string{"vCPUs 2 0 0", "Memory 4.00 GB 0.00 B 0.00 B"},
		Tasks:     []string{},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cards\n got: %+v\nwant: %+v", got, want)
	}

	if drawer := p.text(t, ".v-navigation-drawer"); !strings.Contains(drawer, "Nodes 1/1") || !strings.Contains(drawer, "Services 0/0") {
		t.Errorf("the drawer reads: %s", drawer)
	}

	assertNothingWentWrong(t, p)
}
