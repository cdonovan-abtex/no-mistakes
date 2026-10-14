package daemon

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kunchenguid/no-mistakes/internal/ipc"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

func TestProofLaunchReceiptConflictingPublicDescriptionPreservesFirstObserver(t *testing.T) {
	for _, firstMethod := range []string{ipc.MethodClaimLaunchReceipt, ipc.MethodStartFreshRun} {
		t.Run(firstMethod, func(t *testing.T) {
			p, d := startTestDaemonWithSteps(t, func() []pipeline.Step {
				return []pipeline.Step{&mockPassStep{name: types.StepReview}}
			})
			repo, _ := setupTestGitRepo(t, p, d, "public-receipt-repo")
			const publicPath = "public.json"
			if err := os.WriteFile(filepath.Join(repo.WorkingPath, publicPath), []byte(`{"title":"public","body":"Independent public material"}`), 0o644); err != nil {
				t.Fatal(err)
			}
			gitCmd(t, repo.WorkingPath, "add", publicPath)
			gitCmd(t, repo.WorkingPath, "commit", "-m", "independent public description")
			gitCmd(t, repo.WorkingPath, "push", "gate", "HEAD:refs/heads/main")
			head := gitOutput(t, repo.WorkingPath, "rev-parse", "HEAD")
			client, err := ipc.Dial(p.Socket())
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			const intent = "complete private acceptance intent"
			var pushed ipc.PushReceivedResult
			if err := client.Call(ipc.MethodPushReceived, &ipc.PushReceivedParams{
				Gate: p.RepoDir(repo.ID), Ref: "refs/heads/main", New: head,
				Intent: intent, LaunchNonce: "public-nonce", ValidationGeneration: "generation",
				PublicDescriptionPath: publicPath,
			}, &pushed); err != nil {
				t.Fatal(err)
			}
			claim := func(method, path string) (*ipc.LaunchReceipt, error) {
				if method == ipc.MethodClaimLaunchReceipt {
					var result ipc.ClaimLaunchReceiptResult
					err := client.Call(method, &ipc.ClaimLaunchReceiptParams{
						RepoID: repo.ID, Branch: "main", SubmittedHeadSHA: head,
						LaunchNonce: "public-nonce", ValidationGeneration: "generation", IntentDigest: digestIntent(intent),
						PublicDescriptionPath: path,
					}, &result)
					return result.Receipt, err
				}
				var result ipc.StartFreshRunResult
				err := client.Call(method, &ipc.StartFreshRunParams{
					RepoID: repo.ID, Branch: "main", HeadSHA: head, Intent: intent,
					LaunchNonce: "public-nonce", ValidationGeneration: "generation", PublicDescriptionPath: path,
				}, &result)
				return &result.Receipt, err
			}
			for _, method := range []string{ipc.MethodClaimLaunchReceipt, ipc.MethodStartFreshRun} {
				if _, err := claim(method, "other.json"); err == nil || !strings.Contains(err.Error(), "public description cannot replace") {
					t.Fatalf("conflicting %s error = %v", method, err)
				}
				stored, err := d.GetRun(pushed.RunID)
				if err != nil || stored == nil || stored.LaunchReceiptClaimedAt != nil {
					t.Fatalf("conflicting %s consumed first observer: %+v, err=%v", method, stored, err)
				}
			}
			first, err := claim(firstMethod, publicPath)
			if err != nil || first == nil || first.RunID != pushed.RunID || first.Disposition != "created" {
				t.Fatalf("correct retry = %+v, err=%v", first, err)
			}
			for _, method := range []string{ipc.MethodClaimLaunchReceipt, ipc.MethodStartFreshRun} {
				for _, path := range []string{publicPath, ""} {
					replay, err := claim(method, path)
					if err != nil || replay == nil || replay.RunID != pushed.RunID || replay.Disposition != "reused" {
						t.Fatalf("matching replay %s %q = %+v, err=%v", method, path, replay, err)
					}
				}
			}
			stored, err := d.GetRun(pushed.RunID)
			if err != nil || stored == nil || stored.Intent == nil || *stored.Intent != intent || stored.PublicDescription == nil || stored.PublicDescription.Path != publicPath {
				t.Fatalf("receipt claims changed private intent or public pin: %+v, err=%v", stored, err)
			}
		})
	}
}

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
