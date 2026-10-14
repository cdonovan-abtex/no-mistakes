package steps

import (
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
)

// publicPRIntent gates only the generated Intent section, not other prose or
// evidence derived from intent. Ordinary drafting, fallback content and template
// appendices compose caller-side omission with the trusted repository policy
// here. Pinned-description runs bypass those renderers through
// preparePRPublication's separate source boundary.
//
// The control is tighten-only in both directions:
//
//   - The repository's trusted pr.publish_intent (default true) is the
//     ceiling. A caller can never publish intent on a repository whose
//     trusted config disabled it.
//   - The caller-side decision recorded on the run (runs.omit_intent: the
//     axi run --no-publish-intent flag, or the operator's global
//     intent.publish_intent: false default, folded together at run start)
//     can only remove the section, never restore it.
//
// The repository policy never touches the intent that reaches step prompts.
// The caller-side omission is different: it also WITHHOLDS the intent from the
// PR-drafting turns (prDraftIntentPromptSection), because a PR title or body
// drafted with the intent in context can paraphrase it into the public text,
// and there is deliberately no output filter or prose scanner to catch that.
// Review, test, document, lint, and CI-fix prompts keep the full intent
// under either signal.
func publicPRIntent(sctx *pipeline.StepContext) string {
	if sctx != nil && sctx.Config != nil && !sctx.Config.PR.PublishesIntent() {
		return ""
	}
	if runOmitsIntent(sctx) {
		return ""
	}
	return cleanedUserIntent(sctx)
}

// runOmitsIntent includes both the caller-side section omission and an
// independent public-description pin, which may be adopted after launch.
// Both decisions are durable across recovery and inherited by reruns.
func runOmitsIntent(sctx *pipeline.StepContext) bool {
	return sctx != nil && sctx.Run != nil && (sctx.Run.OmitIntent || sctx.Run.PublicDescription != nil)
}

// prDraftIntentPromptSection is the intent section for the PR-drafting turns
// (ordinary narrative, title-only fallback, and repository-template
// narrative). Under the caller-side omission it is empty, so those turns
// draft from the diff and commit messages only, without paraphrasing the
// withheld input. Other intent-derived evidence is not gated here; see
// preparePRPublication for that boundary. Every other step prompt keeps
// userIntentPromptSection unchanged.
func prDraftIntentPromptSection(sctx *pipeline.StepContext) string {
	if runOmitsIntent(sctx) {
		return ""
	}
	return userIntentPromptSection(sctx)
}
