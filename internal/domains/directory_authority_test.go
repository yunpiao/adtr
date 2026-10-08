package domains

import (
	"context"
	"errors"
	"testing"

	"github.com/yunpiao/adtr/internal/tasks"
)

func TestDirectoryAuthorityRejectsOtherConsumerBeforeDatabase(t *testing.T) {
	p := validDirectoryPins()
	valid := tasks.Task{Kind: DirectoryKindName, PayloadVersion: 1, ActorID: 1, Payload: p.json()}
	for _, change := range []func(*tasks.Task){
		func(t *tasks.Task) { t.Kind = AccountKindName },
		func(t *tasks.Task) { t.PayloadVersion = 2 },
		func(t *tasks.Task) { t.ActorID = 0 },
		func(t *tasks.Task) { t.Payload = nil },
		func(t *tasks.Task) { different := p; different.GrantRevision = "5"; t.Payload = different.json() },
	} {
		task := valid
		change(&task)
		if _, err := (&Store{}).currentDirectory(context.Background(), nil, task, p); !errors.Is(err, tasks.ErrAuthorization) {
			t.Fatal("invalid task reached runtime/database authority", err)
		}
	}
	_, err := (&Store{}).currentDirectory(context.Background(), nil, valid, p)
	var failure *Error
	if !errors.As(err, &failure) || failure.Status != 503 || failure.Code != "directory_read_disabled" {
		t.Fatal("zero deployment did not fail before database", err)
	}
}
