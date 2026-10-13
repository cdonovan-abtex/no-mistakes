package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kunchenguid/no-mistakes/internal/agent"
	"github.com/kunchenguid/no-mistakes/internal/config"
	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/paths"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/publicdescription"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

type publicationAdoptionStep struct {
	name types.StepName
	fn   func(*pipeline.StepContext) (*pipeline.StepOutcome, error)
}

func (s publicationAdoptionStep) Name() types.StepName { return s.name }
func (s publicationAdoptionStep) Execute(ctx *pipeline.StepContext) (*pipeline.StepOutcome, error) {
	return s.fn(ctx)
}

// This is an old, unpinned durable run with an existing owner parked at Document,
// not a fresh replacement. Its ordinary response releases the SAME executor;
// the next real PR step reloads the captured boundary before the actual gh call.
func TestPublicDescriptionOldActiveRunAdoption(t *testing.T) {
	dir, base, head := setupGitRepo(t)
	ag := &mockAgent{name: "test", runFn: func(_ context.Context, _ agent.RunOpts) (*agent.Result, error) {
		raw, _ := json.Marshal(prContent{Title: "private old draft", Body: syntheticPrivateIntent})
		return &agent.Result{Output: raw}, nil
	}}
	sctx := newTestContext(t, ag, dir, base, head, config.Commands{})
	repo, err := sctx.DB.InsertRepo(dir, "https://github.com/test/repo", "main")
	if err != nil {
		t.Fatal(err)
	}
	run, err := sctx.DB.InsertRunWithIntent(repo.ID, "feature", head, base, &db.RunIntent{Summary: syntheticPrivateIntent, Source: db.RunIntentSourceAgent}, "")
	if err != nil {
		t.Fatal(err)
	}
	sctx.Run, sctx.Repo = run, repo
	p := paths.WithRoot(t.TempDir())
	sctx.EvidenceDir = p.RunEvidenceDir("", run.ID)
	if err := os.MkdirAll(sctx.EvidenceDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sctx.EvidenceDir, "private.log"), []byte(syntheticPrivateIntent), 0600); err != nil {
		t.Fatal(err)
	}
	fixture := types.Findings{Items: []types.Finding{}, Summary: syntheticPrivateIntent, TestingSummary: syntheticPrivateIntent,
		TestedHeadSHA: head, Verdict: types.TestVerdictInconclusive,
		Scenarios: []types.TestScenario{{Name: syntheticPrivateIntent, Result: types.ScenarioResultUntested, Reason: "Synthetic old record; no live product validation claimed"}},
		Artifacts: []types.TestArtifact{{Label: syntheticPrivateIntent, Content: syntheticPrivateIntent, Path: filepath.Join(sctx.EvidenceDir, "private.log")}}}
	evidence, _ := json.Marshal(fixture)
	testStep := insertCompletedStep(t, sctx, types.StepTest, string(evidence), "")
	fixSummary := syntheticPrivateIntent
	if _, err := sctx.DB.InsertStepRound(testStep.ID, 2, "auto_fix", nil, &fixSummary, 1); err != nil {
		t.Fatal(err)
	}
	env, logFile := fakeGH(t, "")
	sctx.Config.Test.Evidence.StoreInRepo, sctx.Config.Test.Evidence.AttachMedia = true, true
	document := publicationAdoptionStep{types.StepDocument, func(ctx *pipeline.StepContext) (*pipeline.StepOutcome, error) {
		if ctx.UserIntent != syntheticPrivateIntent {
			return nil, fmt.Errorf("old private intent changed")
		}
		return &pipeline.StepOutcome{NeedsApproval: true, Findings: `{"findings":[{"id":"old-document","severity":"warning","description":"synthetic owner-held gate","action":"ask-user"}],"summary":"synthetic gate"}`}, nil
	}}
	forge := publicationAdoptionStep{types.StepPR, func(ctx *pipeline.StepContext) (*pipeline.StepOutcome, error) {
		if ctx.Run.ID != run.ID || ctx.UserIntent != syntheticPrivateIntent || ctx.Run.PublicDescription == nil {
			return nil, fmt.Errorf("next stage lost original run/intent or adopted pin")
		}
		ctx.Env = env
		return (&PRStep{}).Execute(ctx)
	}}
	executor := pipeline.NewExecutor(sctx.DB, p, sctx.Config, ag, []pipeline.Step{document, forge}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- executor.Execute(ctx, run, repo, dir) }()
	deadline := time.Now().Add(5 * time.Second)
	var before *db.Run
	for time.Now().Before(deadline) {
		before, err = sctx.DB.GetRun(run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if before.AwaitingAgentSince != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if before.AwaitingAgentSince == nil {
		t.Fatal("old Document gate did not park")
	}
	stepsBefore, _ := sctx.DB.GetStepsByRun(run.ID)
	var oldGate *db.StepResult
	for _, s := range stepsBefore {
		if s.StepName == types.StepDocument {
			oldGate = s
		}
	}
	if oldGate == nil {
		t.Fatal("missing original gate")
	}
	roundsBefore, _ := sctx.DB.GetRoundsByStep(oldGate.ID)
	raw, _ := json.Marshal(publicdescription.Content{Title: "fix: INDEPENDENT_PUBLIC_TITLE", Body: syntheticPublicBody})
	// Caller input is independent committable material, not an application edit.
	file := filepath.Join(t.TempDir(), "public.json")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	captured, err := publicdescription.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.AdoptPublicDescription(run.ID, repo.ID, "feature", head, captured); err != nil {
		t.Fatal(err)
	}
	if err := executor.AdoptPublicDescription(run.ID, repo.ID, "feature", head, captured); err != nil {
		t.Fatalf("same capture retry: %v", err)
	}
	if err := executor.AdoptPublicDescription(run.ID, repo.ID, "feature", head, []byte(`{"title":"other","body":"replacement"}`)); err == nil {
		t.Fatal("replacement accepted")
	}
	if err := os.WriteFile(file, []byte(syntheticPrivateIntent), 0600); err != nil {
		t.Fatal(err)
	}
	after, _ := sctx.DB.GetRun(run.ID)
	if after.PublicDescription == nil || after.PublicDescription.RawJSON != string(raw) {
		t.Fatal("capture not durable")
	}
	// This comparison includes all original identity, custody, status, intent,
	// head and parked observability fields, not just the convenient ones.
	comparison := *after
	comparison.PublicDescription = nil
	if !reflect.DeepEqual(*before, comparison) {
		t.Fatalf("adoption changed original run: before=%+v after=%+v", before, after)
	}
	gateAfter, _ := sctx.DB.GetStepResult(oldGate.ID)
	roundsAfter, _ := sctx.DB.GetRoundsByStep(oldGate.ID)
	if !reflect.DeepEqual(oldGate, gateAfter) || !reflect.DeepEqual(roundsBefore, roundsAfter) {
		t.Fatal("adoption changed gate/round/evidence")
	}
	if _, err := os.Stat(filepath.Join(dir, "public.json")); !os.IsNotExist(err) {
		t.Fatal("adoption edited application source")
	}
	select {
	case err := <-done:
		t.Fatalf("adoption resumed old gate: %v", err)
	default:
	}
	// The old owner's ordinary response is still the sole continuation path.
	if err := executor.Respond(types.StepDocument, types.ActionApprove, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("old owner continuation timed out")
	}
	body, err := os.ReadFile(envEntry(env, "FAKE_CLI_PR_BODY_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	log, _ := os.ReadFile(logFile)
	if !strings.Contains(string(body), syntheticPublicBody) || !strings.Contains(string(body), `"verdict":"inconclusive","live":0,"total":1`) || strings.Contains(string(body), "FULL_PRIVATE_SENTINEL") || strings.Contains(string(log), "FULL_PRIVATE_SENTINEL") || !strings.Contains(string(log), "--title fix: INDEPENDENT_PUBLIC_TITLE") {
		t.Fatalf("unsafe/missing old-run emission:\n%s\n%s", body, log)
	}
	if err := after.PublicDescription.Verify(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	t.Logf("same_run=%s submitted=%s description_sha256=%s gate_unchanged=true private_bytes=%d source_untouched=true actual gh boundary:\n%s", run.ID, head, after.PublicDescription.SHA256, len(*after.Intent), log)
}
