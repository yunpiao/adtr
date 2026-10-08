package auth

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/operationallogs"
	"github.com/yunpiao/adtr/internal/tasks"
)

func TestGenericTaskResponsesNeverExposePrivateArtifactMetadata(t *testing.T) {
	task := tasks.Task{ID: "id", Kind: audit.ExportKind, State: tasks.Succeeded, Result: json.RawMessage(`{"sha256":"private-hash","rowCount":42,"artifactToken":"private-token"}`), Cursor: json.RawMessage(`{"snapshotAt":"private-time"}`)}
	for name, v := range map[string]any{"task": task, "detail": tasks.Detail{Task: task}, "list": tasks.List{Tasks: []tasks.Task{task}}, "submit": tasks.Submission{Task: task}, "cancel": map[string]any{"task": task}} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(publicTaskValue(v))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "private-") || strings.Contains(string(raw), "rowCount") {
				t.Fatalf("artifact metadata leaked: %s", raw)
			}
			if !strings.Contains(string(raw), "succeeded") {
				t.Fatal("control state removed")
			}
		})
	}
	task.Kind = "infrastructure.health"
	if string(publicTask(task).Result) != string(task.Result) {
		t.Fatal("public kind altered")
	}
}

func TestOperationalBundleMetadataRequiresDedicatedRoutes(t *testing.T) {
	for _, state := range []tasks.State{tasks.Running, tasks.Succeeded, tasks.Failed, tasks.Cancelled} {
		task := tasks.Task{ID: "bundle", Kind: operationallogs.BundleKindName, State: state,
			Result: json.RawMessage(`{"artifactToken":"private-token","sha256":"private-digest","rowCount":42}`),
			Cursor: json.RawMessage(`{"snapshotAt":"private-time"}`)}
		for name, value := range map[string]any{"detail": tasks.Detail{Task: task}, "list": tasks.List{Tasks: []tasks.Task{task}}, "submission": tasks.Submission{Task: task}, "cancel": map[string]any{"task": task}} {
			t.Run(string(state)+"/"+name, func(t *testing.T) {
				raw, err := json.Marshal(publicTaskValue(value))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(raw), "private-") || strings.Contains(string(raw), "rowCount") || !strings.Contains(string(raw), `"result":{}`) || !strings.Contains(string(raw), `"cursor":{}`) {
					t.Fatalf("generic response exposed bundle metadata: %s", raw)
				}
			})
		}
		if dedicatedTaskRouteCode(task.Kind) != "operational_log_route_required" {
			t.Fatal("bundle admitted through generic control")
		}
	}
}

func TestDomainTaskMetadataRequiresDedicatedResultRoute(t *testing.T) {
	for _, state := range []tasks.State{tasks.Running, tasks.Succeeded, tasks.Failed, tasks.Cancelled} {
		task := tasks.Task{ID: "diagnostic", Kind: domains.KindName, State: state, Result: json.RawMessage(`{"dcHostName":"private-host"}`), Cursor: json.RawMessage(`{"policyRevision":"private-policy"}`)}
		raw, err := json.Marshal(publicTaskValue(tasks.Detail{Task: task}))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "private-") {
			t.Fatal("staged diagnostic leaked")
		}
		if dedicatedTaskRouteCode(task.Kind) != "domain_route_required" {
			t.Fatal("diagnostic generic control admitted")
		}
	}
}
