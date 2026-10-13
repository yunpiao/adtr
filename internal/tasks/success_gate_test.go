package tasks

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestSuccessGateRegistrationCannotExpandGenericKinds(t *testing.T) {
	old := ProductionRegistry().kinds["infrastructure.health"]
	if old.ValidateSuccess != nil {
		t.Fatal("legacy kind changed")
	}
	if _, err := NewRegistry(old); err != nil {
		t.Fatal(err)
	}
	gate := old
	gate.OwnerScoped = true
	gate.CancelDiscardsResult = true
	gate.Schedulable = false
	gate.ValidateSuccess = func(context.Context, pgx.Tx, Task, json.RawMessage) (string, error) { return "", nil }
	if _, err := NewRegistry(gate); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Kind){func(k *Kind) { k.OwnerScoped = false }, func(k *Kind) { k.ReplaySafe = false }, func(k *Kind) { k.CancelDiscardsResult = false }, func(k *Kind) { k.Schedulable = true }} {
		bad := gate
		change(&bad)
		if _, err := NewRegistry(bad); err == nil {
			t.Fatal("unsafe success gate registered")
		}
	}
}
