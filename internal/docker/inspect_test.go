package docker

import (
	"testing"

	"github.com/jtgasper3/swarm-visualizer/internal/config"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/swarm"
)

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
	out := sanitizeTasks([]swarm.Task{newTask()}, &config.Config{HideLabels: []string{"container"}})
	if out[0].Spec.ContainerSpec.Labels != nil {
		t.Fatalf("expected task container labels to be nil, got: %#v", out[0].Spec.ContainerSpec.Labels)
	}

	// "task" is no longer a recognized category and must be a no-op.
	out = sanitizeTasks([]swarm.Task{newTask()}, &config.Config{HideLabels: []string{"task"}})
	if out[0].Spec.ContainerSpec.Labels == nil {
		t.Fatalf("expected task container labels to be retained for unrecognized 'task' category")
	}
}

func TestSanitizeTasks_HideAllEnvs(t *testing.T) {
	tsk := swarm.Task{Spec: swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{Env: []string{"A=1"}, Labels: map[string]string{"l": "v"}}}}
	out := sanitizeTasks([]swarm.Task{tsk}, &config.Config{HideAllEnvs: true})
	if out[0].Spec.ContainerSpec.Env != nil {
		t.Fatalf("expected task envs to be nil, got: %#v", out[0].Spec.ContainerSpec.Env)
	}
}
