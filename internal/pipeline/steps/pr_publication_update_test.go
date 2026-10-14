package steps

import (
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/publicdescription"
	"github.com/kunchenguid/no-mistakes/internal/scm"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

func fullAzureOwnedBody(t *testing.T, head string) string {
	t.Helper()
	appendix := wrapPRAppendix("## Pipeline\n\n" + buildPipelineAttestation(nil, nil, head))
	body := strings.Repeat("x", 4000-scm.PRBodyLen(appendix)-2) + "\n\n" + appendix
	if _, err := parsePROwnedBody(body); err != nil {
		t.Fatal(err)
	}
	if scm.PRBodyLen(body) != 4000 {
		t.Fatal("fixture must fill Azure's 4000-character description budget")
	}
	return body
}

func TestPublicDescriptionUpdatesIgnoreDiscardedBodyBudgets(t *testing.T) {
	for _, mode := range []string{"owned-update", "restamp", "restamp-same-head"} {
		t.Run(mode, func(t *testing.T) {
			sctx, ag := publicDescriptionContext(t, "normal-update")
			recorded := insertCompletedStep(t, sctx, types.StepReview, `{"findings":[],"summary":"FULL_PRIVATE_SENTINEL"}`, "")
			old := fullAzureOwnedBody(t, sctx.Run.HeadSHA)
			host := &attestationTestHost{provider: scm.ProviderAzureDevOps, title: "Author title", body: old}
			pr := &scm.PR{Number: "42"}
			wantHead := sctx.Run.HeadSHA
			if mode == "owned-update" {
				host.body += "\nConcurrent author edit"
				if err := updateOwnedPR(sctx, host, pr, scm.PRContent{Body: old}, "", "", strings.Repeat("discarded legacy evidence", 1000), 4000); err != nil {
					t.Fatal(err)
				}
				if host.reads != 3 || host.title != "fix: INDEPENDENT_PUBLIC_TITLE" {
					t.Fatalf("owned update skipped reconciliation or public title: reads=%d title=%q", host.reads, host.title)
				}
			} else {
				var steps []*db.StepResult
				if mode == "restamp" {
					wantHead = strings.Repeat("ab", 20)
					steps = []*db.StepResult{recorded}
					host.bodyAfterFirstRead = old + "\nConcurrent author edit"
				}
				if err := restampPRAttestationWithSteps(sctx, host, pr, wantHead, steps); err != nil {
					t.Fatal(err)
				}
				if host.updated.Title != "" || host.title != "Author title" {
					t.Fatal("restamp changed the title")
				}
				if mode == "restamp" && host.reads != 4 {
					t.Fatalf("restamp skipped reconciliation: reads=%d", host.reads)
				}
			}
			if host.updates != 1 || len(ag.calls) != 0 || scm.PRBodyLen(host.body) > 4000 {
				t.Fatalf("replacement did not publish once within budget: writes=%d calls=%d size=%d", host.updates, len(ag.calls), scm.PRBodyLen(host.body))
			}
			parts, err := parsePROwnedBody(host.body)
			if err != nil || strings.TrimSpace(parts.before) != syntheticPublicBody {
				t.Fatalf("replacement did not use independent public material: %+v, %v", parts, err)
			}
			if strings.Contains(host.body, "FULL_PRIVATE_SENTINEL") || strings.Contains(host.body, "Concurrent author edit") {
				t.Fatal("discarded private or legacy narrative reached publication")
			}
			if got := parsePipelineAttestationForTest(t, host.body).HeadSHA; got != wantHead {
				t.Fatalf("attestation head = %q, want %q", got, wantHead)
			}
		})
	}
}

func TestPublicDescriptionUpdatesKeepOwnershipAndPublicationGuards(t *testing.T) {
	for _, phase := range []string{"owned-update", "restamp"} {
		for _, mode := range []string{"edited-owned", "concurrent-owned-edit", "replacement-overflow", "missing-marker", "malformed-marker"} {
			if phase == "owned-update" && (mode == "missing-marker" || mode == "malformed-marker") {
				continue
			}
			t.Run(phase+"/"+mode, func(t *testing.T) {
				sctx, _ := publicDescriptionContext(t, "normal-update")
				old := fullAzureOwnedBody(t, sctx.Run.HeadSHA)
				host := &attestationTestHost{provider: scm.ProviderAzureDevOps, body: old}
				damaged := strings.Replace(old, "## Pipeline", "Human note inside evidence", 1)
				switch mode {
				case "edited-owned":
					old, host.body = damaged, damaged
				case "concurrent-owned-edit":
					if phase == "restamp" {
						host.bodyAfterFirstRead = damaged
					} else {
						host.body = damaged
					}
				case "replacement-overflow":
					pin, err := publicdescription.Capture([]byte(`{"title":"public","body":"`+strings.Repeat("public", 1000)+`"}`), sctx.Run.HeadSHA)
					if err != nil {
						t.Fatal(err)
					}
					sctx.Run.PublicDescription = pin
				case "missing-marker":
					host.body = "An ordinary PR without a pipeline attestation"
				case "malformed-marker":
					host.body = pipelineAttestationCommentPrefix + "invalid JSON" + pipelineAttestationCommentClosingToken
				}
				var err error
				if phase == "owned-update" {
					err = updateOwnedPR(sctx, host, &scm.PR{Number: "42"}, scm.PRContent{Body: old}, "", "", "", 4000)
				} else {
					err = restampPRAttestationWithSteps(sctx, host, &scm.PR{Number: "42"}, strings.Repeat("ab", 20), nil)
				}
				wantError := mode != "missing-marker" && mode != "malformed-marker"
				if (err != nil) != wantError || host.updates != 0 {
					t.Fatalf("guard did not refuse publication: err=%v writes=%d", err, host.updates)
				}
			})
		}
	}
}
