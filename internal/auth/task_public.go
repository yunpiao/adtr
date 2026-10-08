package auth

import (
	"encoding/json"
	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/operationallogs"
	"github.com/yunpiao/adtr/internal/tasks"
)

// Generic task controls expose status, not private artifact metadata. Only the
// audit endpoint may disclose snapshot rows/counts/digests after live checks.
func publicTask(t tasks.Task) tasks.Task {
	if t.Kind == audit.ExportKind || t.Kind == operationallogs.BundleKindName || domains.IsConnectionTestKind(t.Kind) || t.Kind == domains.DirectoryKindName {
		t.Result = json.RawMessage(`{}`)
		t.Cursor = json.RawMessage(`{}`)
	}
	return t
}
func publicTaskValue(value any) any {
	switch v := value.(type) {
	case tasks.Task:
		return publicTask(v)
	case tasks.Submission:
		v.Task = publicTask(v.Task)
		return v
	case tasks.Detail:
		v.Task = publicTask(v.Task)
		return v
	case tasks.List:
		for i := range v.Tasks {
			v.Tasks[i] = publicTask(v.Tasks[i])
		}
		return v
	case map[string]any:
		if t, ok := v["task"].(tasks.Task); ok {
			v["task"] = publicTask(t)
		}
		return v
	default:
		return value
	}
}
