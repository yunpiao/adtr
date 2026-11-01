package main

import (
	"context"
	"encoding/json"
	"github.com/yunpiao/adtr/internal/detectionexceptions"
	"time"
)

type exceptionRequest struct {
	Action   string                          `json:"action"`
	Access   detectionexceptions.Access      `json:"access"`
	Snapshot detectionexceptions.Snapshot    `json:"snapshot"`
	Revision uint64                          `json:"expectedRevision"`
	Entry    detectionexceptions.Exception   `json:"entry"`
	Entries  []detectionexceptions.Exception `json:"entries"`
	ID       string                          `json:"id"`
	Event    detectionexceptions.Event       `json:"event"`
	Events   []detectionexceptions.Event     `json:"events"`
	Matches  []detectionexceptions.Match     `json:"matches"`
	AsOf     time.Time                       `json:"asOf"`
}

func exceptions(ctx context.Context, data []byte) (any, error) {
	var in exceptionRequest
	if err := decode(data, &in); err != nil {
		return nil, err
	}
	allowed := map[string][]string{
		"init": {"access"}, "list": {"access", "snapshot"}, "add": {"access", "snapshot", "expectedRevision", "entry"}, "edit": {"access", "snapshot", "expectedRevision", "entry"}, "archive": {"access", "snapshot", "expectedRevision", "id"}, "import": {"access", "snapshot", "expectedRevision", "entries"}, "export": {"access", "snapshot"}, "preview-from-event": {"access", "snapshot", "entry", "event"}, "evaluate": {"access", "snapshot", "events", "matches", "asOf"},
	}
	keys, ok := allowed[in.Action]
	if !ok {
		return nil, errInput
	}
	permit := map[string]bool{"action": true}
	for _, k := range keys {
		permit[k] = true
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, errInput
	}
	for k := range fields {
		if !permit[k] {
			return nil, errInput
		}
	}
	switch in.Action {
	case "init":
		if !in.Access.Read || !in.Access.Write {
			return nil, errInput
		}
		return detectionexceptions.New(in.Access.Scope)
	case "list":
		return detectionexceptions.List(in.Access, in.Snapshot)
	case "add":
		return detectionexceptions.Add(in.Access, in.Snapshot, in.Revision, in.Entry)
	case "edit":
		return detectionexceptions.Edit(in.Access, in.Snapshot, in.Revision, in.Entry)
	case "archive":
		return detectionexceptions.Archive(in.Access, in.Snapshot, in.Revision, in.ID)
	case "import":
		return detectionexceptions.Import(in.Access, in.Snapshot, in.Revision, in.Entries)
	case "export":
		b, err := detectionexceptions.Export(in.Access, in.Snapshot)
		if err != nil {
			return nil, err
		}
		return json.RawMessage(b), nil
	case "preview-from-event":
		return detectionexceptions.PreviewFromEvent(in.Access, in.Snapshot, in.Event, in.Entry.ID, in.Entry.Reason, in.Entry.RuleID, in.Entry.RuleDigest, in.Entry.NotBefore, in.Entry.ExpiresAt)
	case "evaluate":
		return detectionexceptions.Evaluate(ctx, in.Access, in.Snapshot, in.Events, in.Matches, in.AsOf)
	default:
		return nil, errInput
	}
}
