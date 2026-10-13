package daemon

import (
	"reflect"
	"testing"
	"time"

	"github.com/kunchenguid/no-mistakes/internal/ipc"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

func TestAdoptPublicDescriptionRPCLeavesTheExistingGateAndIntentAlone(t *testing.T) {
	p, d := startTestDaemonWithSteps(t, func() []pipeline.Step { return []pipeline.Step{&mockApprovalStep{name: types.StepDocument}} })
	_, head := setupTestGitRepo(t, p, d, "public-adoption-repo")
	client, err := ipc.Dial(p.Socket())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var launched ipc.PushReceivedResult
	if err := client.Call(ipc.MethodPushReceived, &ipc.PushReceivedParams{Gate: p.RepoDir("public-adoption-repo"), Ref: "refs/heads/main", Old: "0000000000000000000000000000000000000000", New: head, Intent: "complete old private acceptance"}, &launched); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		run, err := d.GetRun(launched.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if run.AwaitingAgentSince != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	before, err := d.GetRun(launched.RunID)
	if err != nil || before.AwaitingAgentSince == nil {
		t.Fatal("existing gate did not park", err)
	}
	var result ipc.ProbeOmitIntentResult
	if err := client.Call(ipc.MethodAdoptPublicDescription, &ipc.AdoptPublicDescriptionParams{RepoID: "public-adoption-repo", Branch: "main", RunID: launched.RunID, SubmittedHeadSHA: head, Description: []byte(`{"title":"public title","body":"Independent public material"}`)}, &result); err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatal("capture not acknowledged")
	}
	after, err := d.GetRun(launched.RunID)
	if err != nil || after.PublicDescription == nil {
		t.Fatal("capture not persisted", err)
	}
	comparison := *after
	comparison.PublicDescription = nil
	if !reflect.DeepEqual(*before, comparison) {
		t.Fatal("RPC changed original run/custody/intent/gate")
	}
	var response ipc.RespondResult
	if err := client.Call(ipc.MethodRespond, &ipc.RespondParams{RunID: launched.RunID, Step: types.StepDocument, Action: types.ActionApprove}, &response); err != nil {
		t.Fatal(err)
	}
	finished := waitForRunTerminalState(t, d, launched.RunID)
	if finished.ID != before.ID || finished.Status != types.RunCompleted || *finished.Intent != *before.Intent || finished.PublicDescription == nil {
		t.Fatal("original owner continuation lost run or intent")
	}
	t.Logf("actual_rpc=adopt_public_description same_run=%s private_unchanged=true gate_resumed_by_adoption=false", launched.RunID)
}
