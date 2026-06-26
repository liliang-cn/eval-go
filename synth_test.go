package evalgo

import (
	"context"
	"strings"
	"testing"
)

func TestSynthesizerFromContexts(t *testing.T) {
	// fakeJudge replies identically every call; this reply satisfies the goldens
	// shape (and the evolve shape via its "input" key) so both prompts parse.
	reply := `{"goldens":[{"input":"What is the rate?","expected":"0.30%"},{"input":"And above 50000?","expected":"0.55%"}],"input":"What is the tiered rate and how does it change above 50000?"}`
	sy := Synthesizer{Judge: fakeJudge(reply), PerContext: 2}

	groups := [][]string{{"The savings account pays 0.30% below 50000 and 0.55% above."}}
	samples, err := sy.FromContexts(context.Background(), groups)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 {
		t.Fatalf("want 2 samples, got %d", len(samples))
	}
	s := samples[0]
	if s.Name != "synth-1-1" || s.Input == "" || s.Expected == "" {
		t.Errorf("unexpected sample: %+v", s)
	}
	if len(s.Context) != 1 || s.Rubric == "" {
		t.Errorf("sample should carry context and a default rubric: %+v", s)
	}
}

func TestSynthesizerEvolution(t *testing.T) {
	reply := `{"goldens":[{"input":"What is the rate?","expected":"0.30%"}],"input":"Evolved: reason across both tiers."}`
	sy := Synthesizer{Judge: fakeJudge(reply), PerContext: 1, Evolutions: 1}
	samples, err := sy.FromContexts(context.Background(), [][]string{{"ctx"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 || !strings.HasPrefix(samples[0].Input, "Evolved:") {
		t.Errorf("evolution not applied: %+v", samples)
	}
}

func TestChunkText(t *testing.T) {
	chunks := chunkText("alpha beta gamma delta epsilon", 12)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %v", chunks)
	}
	for _, c := range chunks {
		if len(c) > 12 && !strings.Contains(c, " ") {
			t.Errorf("chunk exceeds size and is unsplit: %q", c)
		}
	}
	// empty input → no chunks
	if got := chunkText("   ", 10); got != nil {
		t.Errorf("want nil for blank input, got %v", got)
	}
}
