package pipeline

import (
	"fmt"

	"github.com/kunchenguid/no-mistakes/internal/publicdescription"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

// AdoptPublicDescription captures a final public description for the SAME
// logical run while its existing pre-push approval gate remains parked. Holding
// the approval mutex makes persistence precede any response/automatic resume.
// No intent, finding, round, gate decision, owner or code is changed.
func (e *Executor) AdoptPublicDescription(runID, repoID, branch, submittedHead string, raw []byte) error {
	pin, err := publicdescription.Capture(raw, submittedHead)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.waiting || e.waitingStep.Order() >= types.StepPush.Order() {
		return fmt.Errorf("public description adoption requires a parked pre-push gate")
	}
	run, err := e.db.GetActiveRun(repoID, branch)
	if err != nil {
		return err
	}
	if run == nil || run.ID != runID || run.SubmittedHeadSHA == nil || *run.SubmittedHeadSHA != submittedHead {
		return fmt.Errorf("active run or submitted head changed; refusing public description adoption")
	}
	parked, err := e.db.GetStepResult(e.waitingStepResultID)
	if err != nil || parked == nil || parked.RunID != runID || (parked.Status != types.StepStatusAwaitingApproval && parked.Status != types.StepStatusFixReview) {
		return fmt.Errorf("parked gate does not belong to the selected run")
	}
	if run.LastPushedSHA != nil || run.PRURL != nil {
		return fmt.Errorf("public description cannot be adopted after publication")
	}
	steps, err := e.db.GetStepsByRun(runID)
	if err != nil {
		return err
	}
	for _, step := range steps {
		if step.StepName.Order() >= types.StepPush.Order() && (step.Status != types.StepStatusPending || step.StartedAt != nil || step.CompletedAt != nil) {
			return fmt.Errorf("public description cannot be adopted after a publication step was encountered")
		}
	}
	if run.PublicDescription != nil {
		if *run.PublicDescription == *pin {
			return nil // lost acknowledgements may retry the identical capture
		}
		return fmt.Errorf("public description cannot replace an existing run pin")
	}
	return e.db.PinRunPublicDescription(runID, pin)
}
