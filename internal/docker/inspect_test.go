package docker

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jtgasper3/swarm-visualizer/internal"
	"github.com/jtgasper3/swarm-visualizer/internal/config"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/swarm"
)

func TestReady(t *testing.T) {
	h := newHub(&config.Config{}, nil)

	if h.Ready() {
		t.Fatal("expected not ready before the first frame is fanned out")
	}

	h.lastFanned = []byte(`{}`)
	if !h.Ready() {
		t.Fatal("expected ready once a frame has been fanned out")
	}
}

func TestSanitizeNodes_HideLabels(t *testing.T) {
	nodes := []swarm.Node{{Spec: swarm.NodeSpec{Annotations: swarm.Annotations{Labels: map[string]string{"secret": "v", "keep": "v2"}}}}}
	cfg := &config.Config{HideLabels: []string{"node"}}
	out := sanitizeNodes(nodes, cfg)
	if out[0].Spec.Labels != nil {
		t.Fatalf("expected labels to be nil, got: %#v", out[0].Spec.Labels)
	}
}

func TestSanitizeServices_HideAllFlags(t *testing.T) {
	svc := swarm.Service{
		Spec: swarm.ServiceSpec{
			TaskTemplate: swarm.TaskSpec{
				ContainerSpec: &swarm.ContainerSpec{
					Env:     []string{"A=1"},
					Configs: []*swarm.ConfigReference{{}},
					Mounts:  []mount.Mount{{Source: "s"}},
					Secrets: []*swarm.SecretReference{{}},
					Labels:  map[string]string{"l": "v"},
				},
			},
		},
	}
	cfg := &config.Config{HideAllEnvs: true, HideAllConfigs: true, HideAllMounts: true, HideAllSecrets: true}
	out := sanitizeServices([]swarm.Service{svc}, cfg)
	cs := out[0].Spec.TaskTemplate.ContainerSpec
	if cs == nil {
		t.Fatalf("expected container spec to exist")
	}
	if cs.Env != nil {
		t.Fatalf("expected Env to be nil, got: %#v", cs.Env)
	}
	if cs.Configs != nil {
		t.Fatalf("expected Configs to be nil, got: %#v", cs.Configs)
	}
	if cs.Mounts != nil {
		t.Fatalf("expected Mounts to be nil, got: %#v", cs.Mounts)
	}
	if cs.Secrets != nil {
		t.Fatalf("expected Secrets to be nil, got: %#v", cs.Secrets)
	}
}

func TestSanitizeServices_HideEnvsLabel(t *testing.T) {
	svc := swarm.Service{
		Spec: swarm.ServiceSpec{
			TaskTemplate: swarm.TaskSpec{
				ContainerSpec: &swarm.ContainerSpec{
					Env: []string{"TEST=1", "OTHER=2"},
				},
			},
			Annotations: swarm.Annotations{Labels: map[string]string{"io.github.jtgasper3.visualizer.hide-envs": "TEST"}},
		},
	}
	out := sanitizeServices([]swarm.Service{svc}, &config.Config{})
	envs := out[0].Spec.TaskTemplate.ContainerSpec.Env
	if len(envs) != 2 {
		t.Fatalf("expected 2 envs, got %d", len(envs))
	}
	if envs[0] != "TEST=(sanitized)" {
		t.Fatalf("expected first env sanitized, got %q", envs[0])
	}
	if envs[1] != "OTHER=2" {
		t.Fatalf("expected second env unchanged, got %q", envs[1])
	}
}

func TestSanitizeServices_HideLabels(t *testing.T) {
	svc := swarm.Service{
		Spec: swarm.ServiceSpec{
			TaskTemplate: swarm.TaskSpec{
				ContainerSpec: &swarm.ContainerSpec{
					Labels: map[string]string{"secret": "v"},
				},
			},
			Annotations: swarm.Annotations{Labels: map[string]string{"secret": "v", "io.github.jtgasper3.visualizer.hide-labels": "secret"}},
		},
	}
	out := sanitizeServices([]swarm.Service{svc}, &config.Config{})
	if val, ok := out[0].Spec.Labels["secret"]; !ok || val != "(sanitized)" {
		t.Fatalf("expected service label 'secret' to be sanitized, got %q, ok=%v", val, ok)
	}
	// container-level
	svc2 := swarm.Service{
		Spec: swarm.ServiceSpec{
			TaskTemplate: swarm.TaskSpec{
				ContainerSpec: &swarm.ContainerSpec{
					Labels: map[string]string{"secret": "v", "io.github.jtgasper3.visualizer.hide-labels": "secret"},
				},
			},
		},
	}
	out2 := sanitizeServices([]swarm.Service{svc2}, &config.Config{})
	if val, ok := out2[0].Spec.TaskTemplate.ContainerSpec.Labels["secret"]; !ok || val != "(sanitized)" {
		t.Fatalf("expected container label 'secret' to be sanitized, got %q, ok=%v", val, ok)
	}
}

func TestSanitizeServices_HideLabelsCategory(t *testing.T) {
	newSvc := func() swarm.Service {
		return swarm.Service{
			Spec: swarm.ServiceSpec{
				TaskTemplate: swarm.TaskSpec{
					ContainerSpec: &swarm.ContainerSpec{
						Labels: map[string]string{"c": "v"},
					},
				},
				Annotations: swarm.Annotations{Labels: map[string]string{"s": "v"}},
			},
		}
	}

	// "service" hides service-level labels but leaves container labels.
	out := sanitizeServices([]swarm.Service{newSvc()}, &config.Config{HideLabels: []string{"service"}})
	if out[0].Spec.Labels != nil {
		t.Fatalf("expected service labels to be nil, got: %#v", out[0].Spec.Labels)
	}
	if out[0].Spec.TaskTemplate.ContainerSpec.Labels == nil {
		t.Fatalf("expected container labels to be retained under 'service'")
	}

	// "container" hides container labels but leaves service-level labels.
	out = sanitizeServices([]swarm.Service{newSvc()}, &config.Config{HideLabels: []string{"container"}})
	if out[0].Spec.TaskTemplate.ContainerSpec.Labels != nil {
		t.Fatalf("expected container labels to be nil, got: %#v", out[0].Spec.TaskTemplate.ContainerSpec.Labels)
	}
	if out[0].Spec.Labels == nil {
		t.Fatalf("expected service labels to be retained under 'container'")
	}

	// "all" hides both.
	out = sanitizeServices([]swarm.Service{newSvc()}, &config.Config{HideLabels: []string{"all"}})
	if out[0].Spec.Labels != nil || out[0].Spec.TaskTemplate.ContainerSpec.Labels != nil {
		t.Fatalf("expected all labels to be nil under 'all'")
	}
}

func TestSanitizeTasks_HideLabelsCategory(t *testing.T) {
	newTask := func() swarm.Task {
		return swarm.Task{Spec: swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{Labels: map[string]string{"c": "v"}}}}
	}

	// "container" hides task container labels.
	out := sanitizeTasks([]swarm.Task{newTask()}, &config.Config{HideLabels: []string{"container"}}, nil)
	if out[0].Spec.ContainerSpec.Labels != nil {
		t.Fatalf("expected task container labels to be nil, got: %#v", out[0].Spec.ContainerSpec.Labels)
	}

	// "task" is no longer a recognized category and must be a no-op.
	out = sanitizeTasks([]swarm.Task{newTask()}, &config.Config{HideLabels: []string{"task"}}, nil)
	if out[0].Spec.ContainerSpec.Labels == nil {
		t.Fatalf("expected task container labels to be retained for unrecognized 'task' category")
	}
}

func TestSanitizeTasks_HideAllEnvs(t *testing.T) {
	tsk := swarm.Task{Spec: swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{Env: []string{"A=1"}, Labels: map[string]string{"l": "v"}}}}
	out := sanitizeTasks([]swarm.Task{tsk}, &config.Config{HideAllEnvs: true}, nil)
	if out[0].Spec.ContainerSpec.Env != nil {
		t.Fatalf("expected task envs to be nil, got: %#v", out[0].Spec.ContainerSpec.Env)
	}
}

// hideEverything turns on every HIDE_* setting, which exercises each branch
// that reaches into a container spec.
func hideEverything() *config.Config {
	return &config.Config{HideAllConfigs: true, HideAllEnvs: true, HideAllMounts: true, HideAllSecrets: true, HideLabels: []string{"all"}}
}

// A service or task using the plugin runtime has no ContainerSpec at all.
func TestSanitize_NoContainerSpec(t *testing.T) {
	pluginSpec := swarm.TaskSpec{Runtime: swarm.RuntimePlugin, PluginSpec: &swarm.RuntimeSpec{Name: "p"}}

	for name, cfg := range map[string]*config.Config{"defaults": {}, "everything hidden": hideEverything()} {
		t.Run(name, func(t *testing.T) {
			services := sanitizeServices([]swarm.Service{{ID: "s1", Spec: swarm.ServiceSpec{TaskTemplate: pluginSpec}}}, cfg)
			if len(services) != 1 || services[0].ID != "s1" {
				t.Errorf("expected the service to pass through, got %#v", services)
			}

			tasks := sanitizeTasks([]swarm.Task{{ID: "t1", ServiceID: "s1", Spec: pluginSpec}}, cfg, hiddenEnvs{"s1": {}})
			if len(tasks) != 1 || tasks[0].ID != "t1" {
				t.Errorf("expected the task to pass through, got %#v", tasks)
			}
		})
	}
}

func TestSanitizeServices_DropsPreviousSpec(t *testing.T) {
	spec := func(env string) swarm.ServiceSpec {
		return swarm.ServiceSpec{
			Annotations:  swarm.Annotations{Labels: map[string]string{"owner": "label-" + env}},
			TaskTemplate: swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{Env: []string{"DB_PASSWORD=" + env}}},
		}
	}
	previous := spec("old-secret")
	svc := swarm.Service{ID: "s1", Spec: spec("new-secret"), PreviousSpec: &previous}

	// Even with nothing configured to be hidden, the previous spec is not sent.
	out := sanitizeServices([]swarm.Service{svc}, &config.Config{})
	if out[0].PreviousSpec != nil {
		t.Fatalf("expected PreviousSpec to be dropped, got %#v", out[0].PreviousSpec)
	}
	frame, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(frame), "old-secret") {
		t.Errorf("previous spec leaked into the frame: %s", frame)
	}
	if !strings.Contains(string(frame), "new-secret") {
		t.Errorf("current spec should be unaffected by default: %s", frame)
	}
}

// HIDE_LABELS removes the service labels, including the hide-envs label
// itself, but the list it holds still has to be applied.
func TestSanitizeServices_HideEnvsLabelSurvivesHideLabels(t *testing.T) {
	for _, category := range []string{"service", "all"} {
		svc := swarm.Service{Spec: swarm.ServiceSpec{
			Annotations:  swarm.Annotations{Labels: map[string]string{hideEnvsLabel: "TEST"}},
			TaskTemplate: swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{Env: []string{"TEST=1", "OTHER=2"}}},
		}}
		out := sanitizeServices([]swarm.Service{svc}, &config.Config{HideLabels: []string{category}})
		if out[0].Spec.Labels != nil {
			t.Errorf("HIDE_LABELS=%s: expected service labels to be nil", category)
		}
		if env := out[0].Spec.TaskTemplate.ContainerSpec.Env; env[0] != "TEST=(sanitized)" || env[1] != "OTHER=2" {
			t.Errorf("HIDE_LABELS=%s: env = %q, want TEST sanitized only", category, env)
		}
	}
}

func TestHiddenEnvNames(t *testing.T) {
	labelled := func(value string) swarm.ServiceSpec {
		return swarm.ServiceSpec{Annotations: swarm.Annotations{Labels: map[string]string{hideEnvsLabel: value}}}
	}
	previous := labelled("OLD")

	names := hiddenEnvNames(&swarm.Service{Spec: labelled(" A , B,,"), PreviousSpec: &previous})
	for _, want := range []string{"A", "B", "OLD"} {
		if _, ok := names[want]; !ok {
			t.Errorf("expected %q in %v", want, names)
		}
	}
	if len(names) != 3 {
		t.Errorf("expected exactly 3 names, got %v", names)
	}

	if names := hiddenEnvNames(&swarm.Service{}); names == nil || len(names) != 0 {
		t.Errorf("expected an empty, non-nil set for an unlabelled service, got %#v", names)
	}
}

func TestSanitizeTasks_HideEnvsFromService(t *testing.T) {
	newTask := func(serviceID string) swarm.Task {
		return swarm.Task{ServiceID: serviceID, Spec: swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{
			Env: []string{"DB_PASSWORD=hunter2", "PLAIN=ok", "BARE"},
		}}}
	}
	hidden := hiddenEnvs{
		"labelled":   {"DB_PASSWORD": {}},
		"unlabelled": {},
	}

	tests := []struct {
		serviceID string
		want      []string
	}{
		{"labelled", []string{"DB_PASSWORD=(sanitized)", "PLAIN=ok", "BARE"}},
		{"unlabelled", []string{"DB_PASSWORD=hunter2", "PLAIN=ok", "BARE"}},
		// No entry: the service is not known, so nothing is shown.
		{"unknown", []string{"DB_PASSWORD=(sanitized)", "PLAIN=(sanitized)", "BARE=(sanitized)"}},
	}
	for _, tc := range tests {
		out := sanitizeTasks([]swarm.Task{newTask(tc.serviceID)}, &config.Config{}, hidden)
		if got := out[0].Spec.ContainerSpec.Env; strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("service %q: env = %q, want %q", tc.serviceID, got, tc.want)
		}
	}
}

// A task carries its own copy of the container labels, hide-labels included.
func TestSanitizeTasks_HideLabelsLabel(t *testing.T) {
	tsk := swarm.Task{ServiceID: "s1", Spec: swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{
		Labels: map[string]string{hideLabelsLabel: "token, absent", "token": "v", "keep": "v"},
	}}}
	labels := sanitizeTasks([]swarm.Task{tsk}, &config.Config{}, hiddenEnvs{"s1": {}})[0].Spec.ContainerSpec.Labels
	if labels["token"] != "(sanitized)" {
		t.Errorf("expected token to be sanitized, got %q", labels["token"])
	}
	if labels["keep"] != "v" {
		t.Errorf("expected keep to be unchanged, got %q", labels["keep"])
	}
	if _, ok := labels["absent"]; ok {
		t.Errorf("expected no entry to be created for a label that was not set: %v", labels)
	}
}

func TestValidateSensitiveDataPaths(t *testing.T) {
	t.Setenv("SENSITIVE_DATA_PATHS", strings.Join([]string{
		// The examples from the README and the compose files.
		"services.*.Spec.TaskTemplate.ContainerSpec.Labels.'desktop.docker.io/mounts/0/Source'",
		"tasks.*.Spec.ContainerSpec.Labels.'desktop.docker.io/mounts/0/Source'",
		// Fields the API types promote from embedded structs.
		"services.*.Spec.Labels.'com.example.token'",
		"networks.*.Labels",
		"nodes.*.UpdatedAt",
	}, ","))
	// LoadConfig prepends the built-in paths, so this covers those as well.
	if err := validateSensitiveDataPaths(config.LoadConfig().SensitiveDataPaths); err != nil {
		t.Errorf("expected the built-in and documented paths to be valid: %v", err)
	}

	err := validateSensitiveDataPaths([]string{
		"services.*.Spec.TaskTemplate.ContainerSpec.Env",
		"services.*.Spec.TaskTemplate.ContainerSpec.Evn",
		"service.*.Spec",
	})
	if err == nil {
		t.Fatal("expected an error for mistyped paths")
	}
	for _, want := range []string{
		`invalid SENSITIVE_DATA_PATHS entry "services.*.Spec.TaskTemplate.ContainerSpec.Evn"`,
		`invalid SENSITIVE_DATA_PATHS entry "service.*.Spec"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not report %s", err, want)
		}
	}
	if strings.Contains(err.Error(), `ContainerSpec.Env"`) {
		t.Errorf("error %q reports a valid path", err)
	}
}

// The API types embed their name, labels, and timestamps, so those fields sit
// one struct deeper than the JSON a path is written against.
func TestSensitiveDataPaths_ReachEmbeddedFields(t *testing.T) {
	data := &SwarmData{
		Networks: []network.Summary{{Network: network.Network{Name: "app", Labels: map[string]string{"k": "net-secret"}}}},
		Services: []swarm.Service{{Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{
			Name:   "web",
			Labels: map[string]string{"com.example.token": "svc-secret", "keep": "kept"},
		}}}},
	}
	for _, path := range []string{"networks.*.Labels", "services.*.Spec.Labels.'com.example.token'"} {
		if err := internal.ClearByPath(data, path); err != nil {
			t.Fatalf("ClearByPath(%q): %v", path, err)
		}
	}

	frame, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, secret := range []string{"net-secret", "svc-secret"} {
		if strings.Contains(string(frame), secret) {
			t.Errorf("%s survived redaction: %s", secret, frame)
		}
	}
	if !strings.Contains(string(frame), "kept") || !strings.Contains(string(frame), `"Name":"web"`) {
		t.Errorf("unrelated fields were cleared: %s", frame)
	}
}
