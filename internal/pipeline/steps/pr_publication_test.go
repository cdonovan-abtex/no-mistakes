package steps

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/agent"
	"github.com/kunchenguid/no-mistakes/internal/config"
	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/publicdescription"
	"github.com/kunchenguid/no-mistakes/internal/scm"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

const syntheticPrivateIntent = "FULL_PRIVATE_SENTINEL\nKeep every acceptance condition, including this second line.\n"
const syntheticPublicBody = "## What Changed\n\nINDEPENDENT_PUBLIC_SENTINEL: separate public material."

func publicDescriptionContext(t *testing.T, mode string) (*pipeline.StepContext, *mockAgent) {
	t.Helper()
	dir, base, _ := setupGitRepo(t)
	raw, _ := json.Marshal(publicdescription.Content{Title: "fix: INDEPENDENT_PUBLIC_TITLE", Body: syntheticPublicBody})
	if err := os.WriteFile(filepath.Join(dir, "public.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", "public.json")
	gitCmd(t, dir, "commit", "-m", "add independent public description")
	head := gitCmd(t, dir, "rev-parse", "HEAD")
	pin, err := publicdescription.Read(context.Background(), dir, head, "public.json")
	if err != nil {
		t.Fatal(err)
	}
	ag := &mockAgent{name: "test", runFn: func(_ context.Context, opts agent.RunOpts) (*agent.Result, error) {
		if mode == "fallback-error" {
			return nil, errors.New("forced draft failure")
		}
		if mode == "fallback-empty" {
			return &agent.Result{Output: json.RawMessage(`{"title":"","body":""}`)}, nil
		}
		// A drafter can misbehave or reuse private context. The final boundary
		// must not publish even paraphrases that a literal scanner could miss.
		payload, _ := json.Marshal(prContent{Title: "fix: FULL_PRIVATE_SENTINEL", Body: "## What Changed\n\n" + syntheticPrivateIntent + "\nUNTRUSTED_PARAPHRASE"})
		return &agent.Result{Output: payload}, nil
	}}
	sctx := newTestContext(t, ag, dir, base, head, config.Commands{})
	repo, err := sctx.DB.InsertRepo(dir, "https://github.com/test/repo", "main")
	if err != nil {
		t.Fatal(err)
	}
	run, err := sctx.DB.InsertRunWithPublicDescription(repo.ID, "feature", head, base, &db.RunIntent{Summary: syntheticPrivateIntent, Source: db.RunIntentSourceAgent}, "", "", "", "", true, nil, pin)
	if err != nil {
		t.Fatal(err)
	}
	// Read through SQLite, as recovery does; no in-memory-only configuration.
	sctx.Run, err = sctx.DB.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	sctx.Repo = repo
	sctx.UserIntent = *sctx.Run.Intent
	if sctx.UserIntent != syntheticPrivateIntent || *sctx.Run.PublicDescription != *pin {
		t.Fatal("durable input lost bytes")
	}
	return sctx, ag
}

// This exercises the real GitHub adapter's process/stdin boundary, not a Host
// echo. fakecli has no network implementation; its readback file is exactly
// the bytes the adapter sent to gh --body-file -. The smoke runner additionally
// denies all sockets to the compiled test process and its children.
func TestPublicDescriptionForgeBoundary(t *testing.T) {
	for _, mode := range []string{"normal-create", "normal-update", "fallback-error", "fallback-empty", "generated-evidence", "owned-update", "ci-restamp"} {
		t.Run(mode, func(t *testing.T) {
			sctx, ag := publicDescriptionContext(t, mode)
			var existing string
			if mode == "normal-update" || mode == "owned-update" || mode == "ci-restamp" {
				existing = "https://github.com/test/repo/pull/99"
			}
			env, logFile := fakeGH(t, existing)
			sctx.Env = env
			bodyFile := envEntry(env, "FAKE_CLI_PR_BODY_FILE")
			if mode == "generated-evidence" || mode == "owned-update" || mode == "ci-restamp" {
				sctx.Config.Test.Evidence.StoreInRepo = true
				sctx.Config.Test.Evidence.AttachMedia = true
				// These deliberately tainted records include every free-text evidence
				// carrier. Inconclusive/zero-live is honest: this fixture is not a live
				// product validation, and the machine record must not claim otherwise.
				evidence := filepath.Join(sctx.EvidenceDir, "private.log")
				if err := os.MkdirAll(sctx.EvidenceDir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(evidence, []byte(syntheticPrivateIntent), 0600); err != nil {
					t.Fatal(err)
				}
				findings := types.Findings{Summary: syntheticPrivateIntent, TestingSummary: syntheticPrivateIntent, RiskLevel: "low", RiskRationale: syntheticPrivateIntent,
					Items:     []types.Finding{{ID: "synthetic", Severity: "warning", Description: syntheticPrivateIntent, Action: "ask-user"}},
					Artifacts: []types.TestArtifact{{Kind: "log", Label: syntheticPrivateIntent, Path: evidence, Content: syntheticPrivateIntent}},
					Tested:    []string{syntheticPrivateIntent}, TestedHeadSHA: sctx.Run.HeadSHA, Verdict: types.TestVerdictInconclusive,
					Scenarios: []types.TestScenario{{Name: syntheticPrivateIntent, Result: types.ScenarioResultUntested, Reason: "Synthetic fixture; no live product validation claimed", Evidence: syntheticPrivateIntent}},
				}
				payload, _ := json.Marshal(findings)
				step := insertCompletedStep(t, sctx, types.StepTest, string(payload), "")
				if err := sctx.DB.UpdateStepStatus(step.ID, types.StepStatusAwaitingApproval); err != nil {
					t.Fatal(err)
				}
				fixSummary := syntheticPrivateIntent
				if _, err := sctx.DB.InsertStepRound(step.ID, 2, "auto_fix", nil, &fixSummary, 1); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "owned-update" || mode == "ci-restamp" {
				old, err := composeOwnedPRContent(prOwnedBody{before: syntheticPrivateIntent}, "private old title", "## Pipeline\n\n"+buildPipelineAttestation(nil, nil, sctx.Run.HeadSHA)+"\n"+syntheticPrivateIntent, 0)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(bodyFile, []byte(old.Body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			step := &PRStep{}
			var err error
			if mode == "ci-restamp" {
				host, reason := buildHost(sctx, scm.ProviderGitHub)
				if host == nil {
					t.Fatal(reason)
				}
				newHead := strings.Repeat("ab", 20)
				err = restampPRAttestationWithSteps(sctx.Ctx, host, &scm.PR{Number: "99", URL: existing}, newHead, nil, sctx.Log, attestationPolicyFrom(sctx), sctx)
			} else {
				_, err = step.Execute(sctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(bodyFile)
			if err != nil {
				t.Fatal(err)
			}
			log, err := os.ReadFile(logFile)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), syntheticPublicBody) || strings.Contains(string(body), "FULL_PRIVATE_SENTINEL") || strings.Contains(string(body), "UNTRUSTED_PARAPHRASE") {
				t.Fatalf("unsafe body:\n%s", body)
			}
			if strings.Contains(string(log), "FULL_PRIVATE_SENTINEL") || !strings.Contains(string(log), "stdin --body ") {
				t.Fatalf("forge boundary not clean/captured:\n%s", log)
			}
			if mode != "ci-restamp" && !strings.Contains(string(log), "--title fix: INDEPENDENT_PUBLIC_TITLE") {
				t.Fatalf("public title missing:\n%s", log)
			}
			if strings.Count(string(body), pipelineAttestationCommentPrefix) != 1 {
				t.Fatal("missing or duplicate attestation")
			}
			if !strings.Contains(string(body), sctx.Run.PublicDescription.SHA256) {
				t.Fatal("description not source-bound")
			}
			if mode == "generated-evidence" && (!strings.Contains(string(body), `"verdict":"inconclusive","live":0,"total":1`) || !strings.Contains(string(body), sctx.Run.HeadSHA)) {
				t.Fatalf("lost genuine recorded verdict/head:\n%s", body)
			}
			if mode == "ci-restamp" && (strings.Contains(string(body), `"live_validation"`) || !strings.Contains(string(body), strings.Repeat("ab", 20))) {
				t.Fatal("restamp claimed stale live validation or wrong head")
			}
			for _, call := range ag.calls {
				if strings.Contains(call.Prompt, "FULL_PRIVATE_SENTINEL") {
					t.Fatal("PR drafter received private intent")
				}
			}
			if !strings.Contains(userIntentPromptSection(sctx), syntheticPrivateIntent) {
				t.Fatal("shared validation intent was changed")
			}
			t.Logf("source=%s description_sha256=%s path=%s actual gh boundary:\n%s", sctx.Run.PublicDescription.HeadSHA, sctx.Run.PublicDescription.SHA256, mode, log)
		})
	}
}

func TestPublicDescriptionKeepsValidationStageInputs(t *testing.T) {
	sctx, ag := publicDescriptionContext(t, "normal-create")
	sctx.IntentSource = db.RunIntentSourceAgent
	sctx.Config.Commands.Test = "printf synthetic-configured-test-evidence"
	sctx.Config.Commands.Lint = "printf synthetic-configured-lint-evidence"
	ag.runFn = func(_ context.Context, opts agent.RunOpts) (*agent.Result, error) {
		if !strings.Contains(opts.Prompt, syntheticPrivateIntent) {
			t.Fatal("validation agent did not receive complete private intent")
		}
		// Use the agent wire shape, including explicitly present empty arrays
		// and scenario fields: types.Findings intentionally omits empty evidence
		// fields and therefore is not a schema-complete Test analyzer fixture.
		payload, _ := json.Marshal(map[string]any{
			"findings": []types.Finding{}, "summary": "synthetic validation fixture",
			"risk_level": "low", "risk_rationale": "Synthetic prompt transport fixture; no live product assessment claimed", "risk_scope": types.FindingsRiskScopeSourceOrExternal,
			"reviewed_paths": []string{"feature.txt", "public.json"}, "verdict": types.TestVerdictInconclusive, "tested_head_sha": sctx.Run.HeadSHA,
			"tested": []string{"Configured printf emitted synthetic baseline evidence"}, "testing_summary": "Synthetic baseline only; no live product validation claimed", "artifacts": []types.TestArtifact{},
			"scenarios": []map[string]any{{"name": "fixture", "result": types.ScenarioResultUntested, "live": false, "evidence": "", "reason": "No live product; prompt transport smoke only"}},
		})
		return &agent.Result{Output: payload}, nil
	}
	for _, stage := range []pipeline.Step{&ReviewStep{}, &TestStep{}, &DocumentStep{}, &LintStep{}} {
		if stage.Name() == types.StepLint {
			// A passing configured lint command has no agent turn. Select the
			// native lint path to exercise its real private-intent transport;
			// Document above kept its separate configured-command duty.
			sctx.Config.Commands.Lint = ""
		}
		before := len(ag.calls)
		if _, err := stage.Execute(sctx); err != nil {
			t.Fatalf("%s: %v", stage.Name(), err)
		}
		if len(ag.calls) == before {
			t.Fatalf("%s made no validation call", stage.Name())
		}
		t.Logf("stage=%s calls=%d full_private_intent_present=true persisted_bytes=%d", stage.Name(), len(ag.calls)-before, len(*sctx.Run.Intent))
	}
	// Exercise the real CI-repair prompt with a no-change conclusion, without
	// publishing a head or invoking any paid model.
	env, _ := fakeGH(t, "https://github.com/test/repo/pull/99")
	sctx.Env = env
	host, reason := buildHost(sctx, scm.ProviderGitHub)
	if host == nil {
		t.Fatal(reason)
	}
	ag.runFn = func(_ context.Context, opts agent.RunOpts) (*agent.Result, error) {
		if !strings.Contains(opts.Prompt, syntheticPrivateIntent) {
			t.Fatal("CI repair lost private intent")
		}
		return &agent.Result{Output: json.RawMessage(`{"summary":"synthetic no-change conclusion","code_change_needed":false}`)}, nil
	}
	before := len(ag.calls)
	if _, err := (&CIStep{}).autoFixCI(sctx, host, &scm.PR{Number: "99"}, ciFixTargets{}); err != nil {
		t.Fatal(err)
	}
	if len(ag.calls) == before {
		t.Fatal("CI repair made no validation call")
	}
	t.Logf("stage=ci-repair calls=%d full_private_intent_present=true persisted_bytes=%d", len(ag.calls)-before, len(*sctx.Run.Intent))
}

func TestPublicDescriptionRefusesChangedPinBeforeForgeWrite(t *testing.T) {
	sctx, _ := publicDescriptionContext(t, "normal-create")
	env, logFile := fakeGH(t, "")
	sctx.Env = env
	host, reason := buildHost(sctx, scm.ProviderGitHub)
	if host == nil {
		t.Fatal(reason)
	}
	sctx.Run.PublicDescription.Body = "altered persisted material"
	if _, err := createPRWithPublication(sctx, host, "feature", "main", scm.PRContent{Title: "fallback", Body: syntheticPrivateIntent}); err == nil {
		t.Fatal("changed pin published")
	}
	log, _ := os.ReadFile(logFile)
	if strings.Contains(string(log), "pr create") {
		t.Fatal("forge was invoked before refusal")
	}
}
