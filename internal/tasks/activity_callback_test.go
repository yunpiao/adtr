package tasks

import (
	"context"
	"testing"
)

type testActivityCandidate struct{}

func (testActivityCandidate) activityCandidate() {}

func TestActivityCandidateCallbackSelection(t *testing.T) {
	for _, ordinary := range []bool{false, true} {
		for _, candidateHook := range []bool{false, true} {
			for _, hasCandidate := range []bool{false, true} {
				regular, special := 0, 0
				w := &Worker{config: WorkerConfig{Owner: "synthetic-worker"}}
				if ordinary {
					w.config.OnActivity = func(context.Context, WorkerActivity) { regular++ }
				}
				if candidateHook {
					w.config.OnActivityCandidate = func(_ context.Context, a WorkerActivity, candidate ActivityCandidate) {
						special++
						if (candidate != nil) != hasCandidate || a.Cycle != "queue" {
							t.Fatal("candidate callback inputs changed")
						}
					}
				}
				var candidate ActivityCandidate
				if hasCandidate {
					candidate = testActivityCandidate{}
				}
				w.activityWithCandidate(context.Background(), "queue", "progress", "", candidate)
				want := 0
				if ordinary || candidateHook {
					want = 1
				}
				if regular+special != want || (candidateHook && regular != 0) {
					t.Fatal("activity event duplicated or dropped")
				}
			}
		}
	}
}
