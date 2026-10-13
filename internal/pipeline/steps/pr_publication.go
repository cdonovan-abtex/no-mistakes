package steps

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/publicdescription"
	"github.com/kunchenguid/no-mistakes/internal/scm"
)

func publicationHead(sctx *pipeline.StepContext) string {
	if sctx == nil || sctx.Run == nil {
		return ""
	}
	return sctx.Run.HeadSHA
}

func hasPublicDescription(sctx *pipeline.StepContext) bool {
	return sctx != nil && sctx.Run != nil && sctx.Run.PublicDescription != nil
}

// publicValidationRecord projects only machine facts from the real recorded
// validation. Findings, summaries, commands, answers, captions and raw artifacts
// are private because validation consumed the complete private intent. Even an
// override's free-text reason must not cross this boundary; its unresolved state
// and trusted opt-in remain visible, with the full reasons retained locally.
func publicValidationRecord(sctx *pipeline.StepContext, head string) (string, error) {
	steps, err := sctx.DB.GetStepsByRun(sctx.Run.ID)
	if err != nil {
		return "", fmt.Errorf("read public validation record: %w", err)
	}
	rounds := make(map[string][]*db.StepRound)
	for _, step := range steps {
		rounds[step.ID], err = sctx.DB.GetRoundsByStep(step.ID)
		if err != nil {
			return "", fmt.Errorf("read public validation rounds: %w", err)
		}
	}
	attestation := newPipelineAttestation(steps, rounds, head, attestationPolicyFrom(sctx))
	for i := range attestation.Steps {
		if attestation.Steps[i].OverrideReason != "" {
			attestation.Steps[i].OverrideReason = "configured test failure approved; reason retained privately"
		}
	}
	if attestation.AllowTestCommandOverride != "" {
		attestation.AllowTestCommandOverride = "trusted test override enabled; reason retained privately"
	}
	raw, err := json.Marshal(attestation)
	if err != nil {
		return "", err
	}
	var table strings.Builder
	table.WriteString("| Step | Recorded status |\n| --- | --- |\n")
	for _, step := range steps {
		status := string(step.Status)
		if (step.OverrideReason != nil && strings.TrimSpace(*step.OverrideReason) != "") || step.TestOverrideReason() != "" {
			status += " (passed-with-override; unresolved condition retained privately)"
		}
		fmt.Fprintf(&table, "| %s | %s |\n", step.StepName, status)
	}
	return "## Pipeline\n\n" + noMistakesPRSignature + "\n\n" + pipelineAttestationCommentPrefix + string(raw) + pipelineAttestationCommentClosingToken + "\n\n" + table.String(), nil
}

// preparePRPublication is the pre-emission boundary for EVERY pipeline forge
// body write. Under the independent-description contract, NONE of the assembled
// draft/live/validation prose is eligible for publication. This is a positive
// source allowlist, not a private-text scanner (which cannot catch paraphrases).
// Ordinary runs retain their existing body/ownership behavior.
func preparePRPublication(sctx *pipeline.StepContext, proposed scm.PRContent, head string, bodyLimit int) (scm.PRContent, error) {
	if !hasPublicDescription(sctx) {
		return proposed, nil
	}
	pin := sctx.Run.PublicDescription
	source, err := publicdescription.Read(sctx.Ctx, sctx.WorkDir, pin.HeadSHA, pin.Path)
	if err != nil {
		return scm.PRContent{}, err
	}
	if source == nil || *source != *pin {
		return scm.PRContent{}, fmt.Errorf("public description source differs from the run pin; refusing publication")
	}
	evidence, err := publicValidationRecord(sctx, head)
	if err != nil {
		return scm.PRContent{}, err
	}
	evidence += fmt.Sprintf("\n\nPublic description source: `%s`, SHA-256 `%s`.", pin.HeadSHA, pin.SHA256)
	evidence = appendIssuesSection(evidence, issuesSection(sctx, source.Body))
	content, err := composeOwnedPRContent(prOwnedBody{before: source.Body}, source.Title, evidence, bodyLimit)
	if err != nil {
		return scm.PRContent{}, err
	}
	if err := verifyClosingIssuesInBody(content.Body, sctx); err != nil {
		return scm.PRContent{}, err
	}
	return scm.PRContent(content), nil
}

func createPRWithPublication(sctx *pipeline.StepContext, host scm.Host, branch, base string, proposed scm.PRContent) (*scm.PR, error) {
	bodyLimit := 0
	if hasPublicDescription(sctx) {
		bodyLimit = scm.HostMaxPRBodyChars(host)
	}
	content, err := preparePRPublication(sctx, proposed, publicationHead(sctx), bodyLimit)
	if err != nil {
		return nil, err
	}
	return host.CreatePR(sctx.Ctx, branch, base, content)
}

func updatePRWithPublication(sctx *pipeline.StepContext, host scm.Host, pr *scm.PR, proposed scm.PRContent) (*scm.PR, error) {
	bodyLimit := 0
	if hasPublicDescription(sctx) {
		bodyLimit = scm.HostMaxPRBodyChars(host)
	}
	content, err := preparePRPublication(sctx, proposed, publicationHead(sctx), bodyLimit)
	if err != nil {
		return nil, err
	}
	return host.UpdatePR(sctx.Ctx, pr, content)
}
