package simulationdetectionschedules

import (
	"encoding/json"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"strings"
	"testing"
	"time"
)

func TestReturnedPayloadCanonicalKeyOrderAndSemanticDrift(t *testing.T) {
	p := Payload{ScheduleID: "schedule", Revision: 1, ScheduledAt: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), Payload: simulationdetection.Payload{TemplateID: "template", TemplateVersion: 1, TemplateHash: strings.Repeat("a", 64), DatasetID: "dataset", FixtureHash: strings.Repeat("b", 64), MappingVersion: simulationdetection.MappingVersion}}
	original, _ := json.Marshal(p)
	var object map[string]any
	if err := json.Unmarshal(original, &object); err != nil {
		t.Fatal(err)
	}
	canonical, _ := json.Marshal(object)
	if string(original) == string(canonical) {
		t.Fatal("test must exercise different field order")
	}
	if !samePayload(original, p) || !samePayload(canonical, p) {
		t.Fatal("equivalent canonical object rejected")
	}
	for _, change := range []struct {
		key   string
		value any
	}{{"scheduleId", "other"}, {"revision", 2}, {"scheduledAt", "2026-11-03T00:00:00Z"}, {"templateId", "other"}, {"templateVersion", 2}, {"templateHash", strings.Repeat("c", 64)}, {"datasetId", "other"}, {"fixtureHash", strings.Repeat("d", 64)}, {"mappingVersion", "other"}} {
		var altered map[string]any
		_ = json.Unmarshal(canonical, &altered)
		altered[change.key] = change.value
		raw, _ := json.Marshal(altered)
		if samePayload(raw, p) {
			t.Fatal("semantic drift accepted", change.key)
		}
	}
	for _, raw := range [][]byte{[]byte(`{}`), append(append([]byte{}, canonical...), []byte(` {}`)...), []byte(strings.TrimSuffix(string(canonical), "}") + `,"unknown":true}`), []byte(`not-json`)} {
		if samePayload(raw, p) {
			t.Fatal("invalid/trailing/unknown payload accepted")
		}
	}
}
