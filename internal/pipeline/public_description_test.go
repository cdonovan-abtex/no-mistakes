package pipeline

import (
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

func TestAdoptPublicDescriptionRefusesUnsafeGates(t *testing.T) {
	for _, mode := range []string{"not-parked", "publication-gate", "already-published", "publication-encountered", "wrong-head", "wrong-branch"} {
		t.Run(mode, func(t *testing.T) {
			database, p, _, repo := setupTest(t)
			head := strings.Repeat("a", 40)
			run, err := database.InsertRunWithIntent(repo.ID, "old-active", head, strings.Repeat("b", 40), &db.RunIntent{Summary: "complete old private intent", Source: db.RunIntentSourceAgent}, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := database.UpdateRunStatus(run.ID, types.RunRunning); err != nil {
				t.Fatal(err)
			}
			sr, err := database.InsertStepResult(run.ID, types.StepDocument)
			if err != nil {
				t.Fatal(err)
			}
			if err := database.ParkStepForApproval(run.ID, sr.ID, types.StepStatusAwaitingApproval, 0, 0, nil); err != nil {
				t.Fatal(err)
			}
			exec := NewExecutor(database, p, nil, nil, nil, nil)
			exec.waiting, exec.waitingStep, exec.waitingStepResultID = true, types.StepDocument, sr.ID
			branch := "old-active"
			switch mode {
			case "not-parked":
				exec.waiting = false
			case "publication-gate":
				exec.waitingStep = types.StepPush
			case "already-published":
				if err := database.UpdateRunPRURL(run.ID, "https://github.com/test/repo/pull/99"); err != nil {
					t.Fatal(err)
				}
			case "publication-encountered":
				pub, err := database.InsertStepResult(run.ID, types.StepPush)
				if err != nil {
					t.Fatal(err)
				}
				if err := database.StartStep(pub.ID); err != nil {
					t.Fatal(err)
				}
			case "wrong-head":
				head = strings.Repeat("c", 40)
			case "wrong-branch":
				branch = "another-branch"
			}
			if err := exec.AdoptPublicDescription(run.ID, repo.ID, branch, head, []byte(`{"title":"public","body":"Independent public material"}`)); err == nil {
				t.Fatal("unsafe adoption accepted")
			}
			got, err := database.GetRun(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.PublicDescription != nil || *got.Intent != "complete old private intent" {
				t.Fatal("refusal mutated publication pin or intent")
			}
		})
	}
}
