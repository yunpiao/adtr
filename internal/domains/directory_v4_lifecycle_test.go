package domains

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/tasks"
	"testing"
	"time"
)

func TestDirectoryV4ExactLifecycleAndNoLegacyAuthority(t *testing.T) {
	s := New(nil)
	kind := s.DirectoryV4Kind()
	tuple, ok := directoryTaskV4.identity()
	if !ok || tuple.dictionaryVersion != 4 || tuple.kind != credentialuse.DirectoryV4Purpose || tuple.purpose != credentialuse.DirectoryV4Purpose {
		t.Fatal("fixed v4 identity mismatch")
	}
	if kind.Name != credentialuse.DirectoryV4Purpose || kind.Version != 1 || kind.MaxAttempts != 1 || !kind.SingleAttemptOnly || kind.ReplaySafe || kind.Schedulable || kind.Platform || kind.OwnerScoped || !kind.CancelDiscardsResult || len(kind.RetryCodes) != 0 || kind.OnQuiesced == nil || kind.Timeout != 125*time.Second {
		t.Fatal("v4 lifecycle widened")
	}
	task := directoryUseTestTask()
	task.Kind = DirectoryV4KindName
	task.Payload = directoryTaskV4.payload(directoryUseTestPins())
	if raw, err := kind.Validate(task.Payload); err != nil || !bytes.Equal(raw, task.Payload) {
		t.Fatal("valid v4 payload", err)
	}
	for _, other := range []directoryTaskProfile{directoryTaskV1, directoryTaskV2, directoryTaskV3, 0, 5} {
		if _, err := validateDirectoryPayloadForProfile(other, task.Payload); err == nil {
			t.Fatal("cross-profile payload accepted")
		}
		if _, err := directoryUseTaskPayloadForProfile(other, task); err == nil {
			t.Fatal("cross-profile use accepted")
		}
	}
	for _, raw := range []json.RawMessage{directoryUseTestTask().Payload, directoryV2UseTestTask().Payload, directoryV3UseTestTask().Payload, bytes.Replace(task.Payload, []byte(`"dictionaryVersion":4`), []byte(`"dictionaryVersion":null`), 1), bytes.Replace(task.Payload, []byte(`"dictionaryVersion":4`), []byte(`"dictionaryVersion":4,"dictionaryVersion":4`), 1)} {
		if _, err := kind.Validate(raw); err == nil {
			t.Fatal("wrong/ambiguous version accepted")
		}
	}
	for _, old := range []tasks.Task{directoryUseTestTask(), directoryV2UseTestTask(), directoryV3UseTestTask(), {}} {
		if _, err := s.currentDirectoryForProfile(context.Background(), nil, directoryTaskV4, old, directoryUseTestPins()); !errors.Is(err, tasks.ErrAuthorization) {
			t.Fatal("foreign task reached SQL", err)
		}
	}
	if s.directoryEnabled(directoryTaskV4) {
		t.Fatal("nil runtime enabled v4")
	}
}
