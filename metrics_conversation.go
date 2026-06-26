package evalgo

import (
	"context"
	"fmt"
	"strings"
)

// Conversation metrics evaluate a multi-turn chat history carried in the Sample
// (Turns, Persona) — the conversational counterpart to DeepEval's multi-turn
// metrics. Each renders the recorded dialogue for a judge:
//
//   - ConversationCompleteness: were all of the user's intentions fulfilled?
//   - KnowledgeRetention:       did the assistant remember what the user told it?
//   - ConversationRelevancy:    is each assistant message relevant in context?
//   - RoleAdherence:            did the assistant stay in its assigned persona?
//
// All skip (pass) when the sample carries no conversation.

// formatTurns renders a conversation as "user: ...\nassistant: ..." for a judge prompt.
func formatTurns(turns []Turn) string {
	var b strings.Builder
	for _, t := range turns {
		fmt.Fprintf(&b, "%s: %s\n", t.Role, t.Content)
	}
	return b.String()
}

// ConversationCompleteness judges whether the assistant fulfilled all of the
// user's intentions and requests across the whole conversation.
func ConversationCompleteness(judge JudgeFunc, passThreshold float64) Metric {
	return MetricFunc{"conversation_completeness", func(ctx context.Context, s Sample) (Result, error) {
		if len(s.Turns) == 0 {
			return Result{Metric: "conversation_completeness", Passed: true, Score: 1, Reason: "no conversation (skipped)"}, nil
		}
		prompt := fmt.Sprintf(`Judge whether the ASSISTANT fulfilled ALL of the user's intentions and requests across
the whole CONVERSATION. A low score means some request was ignored, left unanswered, or only partly handled.
Return STRICTLY JSON and nothing else:
{"passed": <bool>, "score": <number 0.0-1.0>, "reason": "<short justification>"}

CONVERSATION:
%s

JSON:`, formatTurns(s.Turns))
		v, err := callVerdict(ctx, judge, prompt)
		if err != nil {
			return Result{}, err
		}
		passed := v.Passed && v.Score >= passThreshold
		return Result{Metric: "conversation_completeness", Score: v.Score, Passed: passed, Reason: v.Reason}, nil
	}}
}

// KnowledgeRetention judges whether the assistant RETAINED information the user
// already provided earlier. A low score means it asked again for facts already
// given, or forgot or contradicted earlier context.
func KnowledgeRetention(judge JudgeFunc, passThreshold float64) Metric {
	return MetricFunc{"knowledge_retention", func(ctx context.Context, s Sample) (Result, error) {
		if len(s.Turns) == 0 {
			return Result{Metric: "knowledge_retention", Passed: true, Score: 1, Reason: "no conversation (skipped)"}, nil
		}
		prompt := fmt.Sprintf(`Judge whether the ASSISTANT RETAINED information the user already provided earlier in the
CONVERSATION. A low score means it asked again for facts already given, or forgot or contradicted earlier context.
Return STRICTLY JSON and nothing else:
{"passed": <bool>, "score": <number 0.0-1.0>, "reason": "<short justification>"}

CONVERSATION:
%s

JSON:`, formatTurns(s.Turns))
		v, err := callVerdict(ctx, judge, prompt)
		if err != nil {
			return Result{}, err
		}
		passed := v.Passed && v.Score >= passThreshold
		return Result{Metric: "knowledge_retention", Score: v.Score, Passed: passed, Reason: v.Reason}, nil
	}}
}

// ConversationRelevancy asks the judge, for EACH assistant message, whether it
// is relevant given the conversation so far. Score = relevant / total.
func ConversationRelevancy(judge JudgeFunc, passThreshold float64) Metric {
	return MetricFunc{"conversation_relevancy", func(ctx context.Context, s Sample) (Result, error) {
		if len(s.Turns) == 0 {
			return Result{Metric: "conversation_relevancy", Passed: true, Score: 1, Reason: "no conversation (skipped)"}, nil
		}
		var assistant []string
		for _, t := range s.Turns {
			if strings.EqualFold(t.Role, "assistant") {
				assistant = append(assistant, t.Content)
			}
		}
		if len(assistant) == 0 {
			return Result{Metric: "conversation_relevancy", Passed: true, Score: 1, Reason: "no assistant turns (skipped)"}, nil
		}
		var b strings.Builder
		for i, msg := range assistant {
			fmt.Fprintf(&b, "%d. %s\n", i+1, msg)
		}
		var resp struct {
			Verdicts []struct {
				Verdict string `json:"verdict"` // yes | no
				Reason  string `json:"reason"`
			} `json:"verdicts"`
		}
		prompt := fmt.Sprintf(`For EACH numbered ASSISTANT message, decide whether it is relevant given the conversation
so far. Answer "no" if a message is off-topic, ignores the user, or does not belong in context.
Return STRICTLY JSON: {"verdicts":[{"verdict":"yes|no","reason":"only if no"}]} one per assistant message, in order.

CONVERSATION:
%s

ASSISTANT MESSAGES:
%s

JSON:`, formatTurns(s.Turns), b.String())
		if err := callJSON(ctx, judge, prompt, &resp); err != nil {
			return Result{}, err
		}
		relevant, total := 0, len(assistant)
		for _, v := range resp.Verdicts {
			if !strings.EqualFold(v.Verdict, "no") {
				relevant++
			}
		}
		if len(resp.Verdicts) < total { // judge returned fewer; treat remainder as relevant
			relevant += total - len(resp.Verdicts)
		}
		score := float64(relevant) / float64(total)
		reason := fmt.Sprintf("%d/%d assistant turns relevant", relevant, total)
		return Result{Metric: "conversation_relevancy", Score: score, Passed: score >= passThreshold, Reason: reason}, nil
	}}
}

// RoleAdherence judges whether the assistant consistently stayed in its assigned
// Persona throughout the conversation. Skipped (pass) when no persona is given.
func RoleAdherence(judge JudgeFunc, passThreshold float64) Metric {
	return MetricFunc{"role_adherence", func(ctx context.Context, s Sample) (Result, error) {
		if strings.TrimSpace(s.Persona) == "" {
			return Result{Metric: "role_adherence", Passed: true, Score: 1, Reason: "no persona (skipped)"}, nil
		}
		if len(s.Turns) == 0 {
			return Result{Metric: "role_adherence", Passed: true, Score: 1, Reason: "no conversation (skipped)"}, nil
		}
		prompt := fmt.Sprintf(`The ASSISTANT should consistently act as the following PERSONA. Judge from the CONVERSATION
whether the assistant stayed in that role throughout. A low score means it broke character, contradicted the
persona, or behaved as a different role.
Return STRICTLY JSON and nothing else:
{"passed": <bool>, "score": <number 0.0-1.0>, "reason": "<short justification>"}

PERSONA:
%s

CONVERSATION:
%s

JSON:`, s.Persona, formatTurns(s.Turns))
		v, err := callVerdict(ctx, judge, prompt)
		if err != nil {
			return Result{}, err
		}
		passed := v.Passed && v.Score >= passThreshold
		return Result{Metric: "role_adherence", Score: v.Score, Passed: passed, Reason: v.Reason}, nil
	}}
}
