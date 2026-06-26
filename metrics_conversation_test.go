package evalgo

import (
	"context"
	"testing"
)

func convoSample() Sample {
	return Sample{
		Persona: "a friendly travel agent",
		Turns: []Turn{
			{Role: "user", Content: "I want to fly to Tokyo next month."},
			{Role: "assistant", Content: "Great! Which city are you departing from?"},
			{Role: "user", Content: "From Berlin."},
			{Role: "assistant", Content: "I found several Berlin to Tokyo flights for next month."},
		},
	}
}

func TestConversationCompleteness(t *testing.T) {
	m := ConversationCompleteness(fakeJudge(`{"passed":true,"score":0.9,"reason":"ok"}`), 0.7)
	res, err := m.Score(context.Background(), convoSample())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || res.Score != 0.9 {
		t.Errorf("got passed=%v score=%.2f", res.Passed, res.Score)
	}

	// empty turns → skipped pass
	res2, _ := m.Score(context.Background(), Sample{})
	if !res2.Passed || res2.Score != 1 {
		t.Errorf("expected skip-pass, got passed=%v score=%.2f", res2.Passed, res2.Score)
	}
}

func TestKnowledgeRetention(t *testing.T) {
	m := KnowledgeRetention(fakeJudge(`{"passed":true,"score":0.9,"reason":"ok"}`), 0.7)
	res, err := m.Score(context.Background(), convoSample())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || res.Score != 0.9 {
		t.Errorf("got passed=%v score=%.2f", res.Passed, res.Score)
	}
}

func TestConversationRelevancy(t *testing.T) {
	// convoSample has two assistant turns → two verdicts.
	m := ConversationRelevancy(fakeJudge(`{"verdicts":[{"verdict":"yes"},{"verdict":"yes"}]}`), 0.7)
	res, err := m.Score(context.Background(), convoSample())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || res.Score != 1 {
		t.Errorf("got passed=%v score=%.2f reason=%s", res.Passed, res.Score, res.Reason)
	}
}

func TestRoleAdherence(t *testing.T) {
	m := RoleAdherence(fakeJudge(`{"passed":true,"score":0.9,"reason":"ok"}`), 0.7)
	res, err := m.Score(context.Background(), convoSample())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || res.Score != 0.9 {
		t.Errorf("got passed=%v score=%.2f", res.Passed, res.Score)
	}
}

func TestRoleAdherenceSkipWithoutPersona(t *testing.T) {
	// A nil judge must never be called when there is no persona to evaluate.
	m := RoleAdherence(nil, 0.7)
	res, err := m.Score(context.Background(), Sample{Turns: []Turn{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || res.Score != 1 {
		t.Errorf("expected skip-pass without a persona, got passed=%v score=%.2f", res.Passed, res.Score)
	}
}
