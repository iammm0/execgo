package unit_test

import (
	"testing"

	"github.com/iammm0/execgo/pkg/models"
)

func TestTaskGraphValidate(t *testing.T) {
	tests := []struct {
		name    string
		graph   models.TaskGraph
		wantErr bool
	}{
		{
			name:    "empty graph",
			graph:   models.TaskGraph{},
			wantErr: true,
		},
		{
			name: "duplicate id",
			graph: models.TaskGraph{Tasks: []*models.Task{
				{ID: "a", Type: "noop"},
				{ID: "a", Type: "noop"},
			}},
			wantErr: true,
		},
		{
			name: "unknown dependency",
			graph: models.TaskGraph{Tasks: []*models.Task{
				{ID: "a", Type: "noop", DependsOn: []string{"x"}},
			}},
			wantErr: true,
		},
		{
			name: "self dependency",
			graph: models.TaskGraph{Tasks: []*models.Task{
				{ID: "a", Type: "noop", DependsOn: []string{"a"}},
			}},
			wantErr: true,
		},
		{
			name: "cycle",
			graph: models.TaskGraph{Tasks: []*models.Task{
				{ID: "a", Type: "noop", DependsOn: []string{"b"}},
				{ID: "b", Type: "noop", DependsOn: []string{"a"}},
			}},
			wantErr: true,
		},
		{
			name: "valid dag",
			graph: models.TaskGraph{Tasks: []*models.Task{
				{ID: "a", Type: "noop"},
				{ID: "b", Type: "noop", DependsOn: []string{"a"}},
			}},
			wantErr: false,
		},
		{
			name: "valid required capabilities",
			graph: models.TaskGraph{Tasks: []*models.Task{
				{ID: "a", Type: "noop", RequiredCapabilities: map[string]string{"sandbox": "docker"}},
			}},
			wantErr: false,
		},
		{
			name: "empty capability key",
			graph: models.TaskGraph{Tasks: []*models.Task{
				{ID: "a", Type: "noop", RequiredCapabilities: map[string]string{" ": "docker"}},
			}},
			wantErr: true,
		},
		{
			name: "empty capability value",
			graph: models.TaskGraph{Tasks: []*models.Task{
				{ID: "a", Type: "noop", RequiredCapabilities: map[string]string{"sandbox": " "}},
			}},
			wantErr: true,
		},
		{
			name: "executor capability mismatch",
			graph: models.TaskGraph{Tasks: []*models.Task{
				{ID: "a", Type: "noop", RequiredCapabilities: map[string]string{"executor": "os"}},
			}},
			wantErr: true,
		},
		{
			name: "executor capability token match",
			graph: models.TaskGraph{Tasks: []*models.Task{
				{ID: "a", Type: "noop", RequiredCapabilities: map[string]string{"executor": "os, noop"}},
			}},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.graph.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestCapabilityValueMatches(t *testing.T) {
	if !models.CapabilityValueMatches("os, noop", "noop") {
		t.Fatal("expected comma-separated worker capability to match token")
	}
	if models.CapabilityValueMatches("os,noop", "http") {
		t.Fatal("unexpected match for missing capability token")
	}
}

func TestEffectiveCapabilityRequirements(t *testing.T) {
	reqs := models.EffectiveCapabilityRequirements(&models.Task{
		Type:                 "noop",
		RequiredCapabilities: map[string]string{"sandbox": "docker"},
	})
	if reqs["executor"] != "noop" {
		t.Fatalf("executor requirement=%q want noop", reqs["executor"])
	}
	if reqs["sandbox"] != "docker" {
		t.Fatalf("sandbox requirement=%q want docker", reqs["sandbox"])
	}
}
