package domains

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/tasks"
)

func directoryV2UseTestTask() tasks.Task {
	task := directoryUseTestTask()
	task.Kind = DirectoryV2KindName
	task.Payload = (directoryV2PinnedPayload{directoryUseTestPins(), 2}).json()
	return task
}

func TestDirectoryProfilesKeepLegacyPayloadGoldenAndExactIdentity(t *testing.T) {
	golden := `{"credentialSource":"operation_account","connectionRevision":"2","connectionCredentialGeneration":"2","accountId":"account-id","accountCredentialRevision":"1","grantRoleId":"platform_admin","grantRevision":"9","policyRevision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
	if string(directoryUseTestPins().json()) != golden {
		t.Fatal("legacy eight-field payload changed")
	}
	for _, profile := range []directoryTaskProfile{directoryTaskV1, directoryTaskV2} {
		task := directoryUseTestTask()
		purpose, dictionary := credentialuse.DirectoryPurpose, 1
		if profile == directoryTaskV2 {
			task, purpose, dictionary = directoryV2UseTestTask(), credentialuse.DirectoryV2Purpose, 2
		}
		identity, ok := profile.identity()
		if !ok || identity.kind != task.Kind || identity.purpose != purpose || identity.dictionaryVersion != dictionary {
			t.Fatal("profile tuple mismatch")
		}
		canonical, err := validateDirectoryPayloadForProfile(profile, task.Payload)
		if err != nil || !bytes.Equal(canonical, task.Payload) {
			t.Fatal("payload bytes changed", err)
		}
		if pins, err := directoryUseTaskPayloadForProfile(profile, task); err != nil || pins != directoryUseTestPins() {
			t.Fatal("exact profile rejected", err)
		}
		for _, other := range []directoryTaskProfile{0, directoryTaskV1, directoryTaskV2, 3, 255} {
			if other == profile {
				continue
			}
			if _, err := validateDirectoryPayloadForProfile(other, task.Payload); err == nil {
				t.Fatal("cross-profile payload accepted", profile, other)
			}
			if _, err := directoryUseTaskPayloadForProfile(other, task); !errors.Is(err, errDirectoryUseEvidence) {
				t.Fatal("cross-profile task accepted", profile, other, err)
			}
		}
	}
	if _, err := directoryUseTaskPayload(directoryV2UseTestTask()); !errors.Is(err, errDirectoryUseEvidence) {
		t.Fatal("old cleanup validator widened", err)
	}
}

func TestDirectoryV2PayloadRejectsDictionaryAndPinAmbiguity(t *testing.T) {
	raw := directoryV2UseTestTask().Payload
	for name, value := range map[string]string{
		"omitted":    strings.Replace(string(raw), `,"dictionaryVersion":2`, "", 1),
		"legacy":     strings.Replace(string(raw), `"dictionaryVersion":2`, `"dictionaryVersion":1`, 1),
		"unknown":    strings.Replace(string(raw), `"dictionaryVersion":2`, `"dictionaryVersion":3`, 1),
		"null":       strings.Replace(string(raw), `"dictionaryVersion":2`, `"dictionaryVersion":null`, 1),
		"string":     strings.Replace(string(raw), `"dictionaryVersion":2`, `"dictionaryVersion":"2"`, 1),
		"fractional": strings.Replace(string(raw), `"dictionaryVersion":2`, `"dictionaryVersion":2.0`, 1),
		"exponent":   strings.Replace(string(raw), `"dictionaryVersion":2`, `"dictionaryVersion":2e0`, 1),
		"duplicate":  strings.Replace(string(raw), `"dictionaryVersion":2`, `"dictionaryVersion":2,"dictionaryVersion":2`, 1),
		"extra":      strings.Replace(string(raw), `"dictionaryVersion":2`, `"dictionaryVersion":2,"purpose":"domain.directory_read.v2"`, 1),
		"case":       strings.Replace(string(raw), `"dictionaryVersion":2`, `"DictionaryVersion":2`, 1),
		"oversize":   string(raw) + strings.Repeat(" ", 2048),
	} {
		t.Run(name, func(t *testing.T) {
			if out, err := validateDirectoryV2Payload([]byte(value)); err == nil || out != nil {
				t.Fatal("invalid payload accepted")
			}
		})
	}
	for field, value := range map[string]any{
		"credentialSource": "inline", "connectionRevision": "0", "connectionCredentialGeneration": "01", "accountId": "", "accountCredentialRevision": "-1", "grantRoleId": "viewer", "grantRevision": "1e2", "policyRevision": strings.Repeat("A", 64),
	} {
		t.Run(field, func(t *testing.T) {
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			body[field] = value
			bad, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			if out, err := validateDirectoryV2Payload(bad); err == nil || out != nil {
				t.Fatal("invalid immutable pin accepted")
			}
			delete(body, field)
			bad, _ = json.Marshal(body)
			if _, err := validateDirectoryV2Payload(bad); err == nil {
				t.Fatal("missing immutable pin accepted")
			}
		})
	}
}

func TestDirectoryProfileBoundariesFailBeforeSQL(t *testing.T) {
	ctx, store := context.Background(), New(nil)
	for _, profile := range []directoryTaskProfile{0, directoryTaskV1, directoryTaskV2, 3} {
		for _, task := range []tasks.Task{directoryUseTestTask(), directoryV2UseTestTask()} {
			identity, ok := profile.identity()
			if ok && identity.kind == task.Kind {
				continue
			}
			if err := reserveDirectoryUseForProfileTx(ctx, nil, profile, task, directoryUseTestPins()); !errors.Is(err, errDirectoryUseEvidence) {
				t.Fatal("wrong reservation reached SQL", err)
			}
			task.State, task.Attempt, task.LeaseOwner, task.FencingToken = tasks.Running, 1, "owner", 7
			if err := openDirectoryUseForProfileTx(ctx, nil, profile, task, directoryUseTestPins()); !errors.Is(err, errDirectoryUseEvidence) {
				t.Fatal("wrong opening reached SQL", err)
			}
		}
		if err := store.acknowledgeDirectoryUseForProfile(ctx, nil, profile, tasks.QuiescedAttempt{}); !errors.Is(err, errDirectoryUseEvidence) {
			t.Fatal("unissued witness accepted", err)
		}
	}
	for _, profile := range []directoryTaskProfile{0, 3} {
		if _, _, err := lockDirectoryUseForProfileTx(ctx, nil, profile, "tenant", "task"); !errors.Is(err, errDirectoryUseEvidence) {
			t.Fatal("unknown profile reached lock SQL")
		}
		if _, err := store.reconcileReservedDirectoryUsesForProfile(ctx, nil, profile, 1); !errors.Is(err, errDirectoryUseEvidence) {
			t.Fatal("unknown profile reached maintenance")
		}
		if _, err := reconcileReservedDirectoryUseForProfile(ctx, nil, profile, "tenant", "task"); !errors.Is(err, errDirectoryUseEvidence) {
			t.Fatal("unknown profile reached reconciliation")
		}
	}
	v2 := directoryV2UseTestTask()
	if err := reserveDirectoryUseTx(ctx, nil, v2, directoryUseTestPins()); !errors.Is(err, errDirectoryUseEvidence) {
		t.Fatal("old reservation wrapper accepted v2")
	}
	v2.State, v2.Attempt, v2.LeaseOwner, v2.FencingToken = tasks.Running, 1, "owner", 7
	if err := openDirectoryUseTx(ctx, nil, v2, directoryUseTestPins()); !errors.Is(err, errDirectoryUseEvidence) {
		t.Fatal("old opening wrapper accepted v2")
	}
}

func TestDirectoryV2CleanupIgnoresMutableAuthorityAndRequiresImmutableTuple(t *testing.T) {
	original := directoryV2UseTestTask()
	for _, state := range []tasks.State{tasks.Queued, tasks.Running, tasks.CancelRequested, tasks.Failed, tasks.Cancelled} {
		task := original
		task.State, task.LeaseOwner, task.FencingToken, task.Attempt = state, "later-owner", 88, 8
		task.AuthorizationVersion, task.Archived = "later-epoch", true
		if _, err := directoryUseTaskPayloadForProfile(directoryTaskV2, task); err != nil {
			t.Fatal("cleanup depends on live authority", err)
		}
	}
	for name, change := range map[string]func(*tasks.Task){
		"kind": func(t *tasks.Task) { t.Kind = DirectoryKindName }, "version": func(t *tasks.Task) { t.PayloadVersion = 2 }, "attempts": func(t *tasks.Task) { t.MaxAttempts = 2 }, "parent": func(t *tasks.Task) { t.ParentID = "parent" }, "hash": func(t *tasks.Task) { t.PayloadHash = "" }, "v1-body": func(t *tasks.Task) { t.Payload = directoryUseTestPins().json() },
	} {
		t.Run(name, func(t *testing.T) {
			task := original
			change(&task)
			if _, err := directoryUseTaskPayloadForProfile(directoryTaskV2, task); !errors.Is(err, errDirectoryUseEvidence) {
				t.Fatal("invalid cleanup identity accepted", err)
			}
		})
	}
}
