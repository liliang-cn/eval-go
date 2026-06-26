package evalgo

import (
	"context"
	"fmt"
	"strings"
)

// Synthesizer generates golden Samples with an LLM, so you can build an
// evaluation dataset from source documents instead of hand-writing JSON. The
// generated Samples carry Input / Expected / Context (and a default Rubric),
// ready to feed straight into a Suite — including the RAG and faithfulness
// metrics that score against Context.
//
// It uses only a JudgeFunc, so the stdlib-only core stays dependency-free.
type Synthesizer struct {
	Judge      JudgeFunc // the generating LLM (same shape as a judge)
	PerContext int       // goldens to generate per context group (default 2)
	Evolutions int       // complexity-evolution passes applied to each question (default 0)
	Rubric     string    // rubric stamped on every Sample (default: grounded-answer criterion)
}

// golden is the shape the generator LLM is asked to return.
type golden struct {
	Input    string `json:"input"`
	Expected string `json:"expected"`
}

// FromContexts generates Samples for each group of retrieval-context chunks: the
// LLM writes realistic questions answerable from that context plus grounded
// reference answers. Each group becomes that many Samples' Context.
func (sy Synthesizer) FromContexts(ctx context.Context, groups [][]string) ([]Sample, error) {
	per := sy.PerContext
	if per <= 0 {
		per = 2
	}
	rubric := sy.Rubric
	if rubric == "" {
		rubric = "Accurately answers the question using only the provided context."
	}

	var out []Sample
	for gi, group := range groups {
		if len(group) == 0 {
			continue
		}
		goldens, err := sy.genGoldens(ctx, group, per)
		if err != nil {
			return nil, fmt.Errorf("context group %d: %w", gi, err)
		}
		for i, g := range goldens {
			input := strings.TrimSpace(g.Input)
			if input == "" {
				continue
			}
			for e := 0; e < sy.Evolutions; e++ {
				evolved, err := sy.evolve(ctx, input, group)
				if err != nil {
					return nil, fmt.Errorf("context group %d evolve: %w", gi, err)
				}
				if evolved != "" {
					input = evolved
				}
			}
			out = append(out, Sample{
				Name:     fmt.Sprintf("synth-%d-%d", gi+1, i+1),
				Input:    input,
				Expected: strings.TrimSpace(g.Expected),
				Context:  group,
				Rubric:   rubric,
			})
		}
	}
	return out, nil
}

// FromDocuments splits each document into ~chunkSize-rune chunks (on word
// boundaries), treats each chunk as one context group, and generates from them.
func (sy Synthesizer) FromDocuments(ctx context.Context, docs []string, chunkSize int) ([]Sample, error) {
	if chunkSize <= 0 {
		chunkSize = 1000
	}
	var groups [][]string
	for _, doc := range docs {
		for _, chunk := range chunkText(doc, chunkSize) {
			groups = append(groups, []string{chunk})
		}
	}
	return sy.FromContexts(ctx, groups)
}

// genGoldens asks the LLM for `n` question/answer pairs grounded in the context.
func (sy Synthesizer) genGoldens(ctx context.Context, group []string, n int) ([]golden, error) {
	var resp struct {
		Goldens []golden `json:"goldens"`
	}
	prompt := fmt.Sprintf(`You are building an evaluation dataset. Using ONLY the CONTEXT below, write %d distinct,
realistic user questions that are fully answerable from the context, each paired with a concise,
factually-grounded expected answer. Do not invent facts beyond the context.
Return STRICTLY JSON: {"goldens":[{"input":"<question>","expected":"<answer>"}]}

CONTEXT:
%s

JSON:`, n, joinContext(group))
	if err := callJSON(ctx, sy.Judge, prompt, &resp); err != nil {
		return nil, err
	}
	return resp.Goldens, nil
}

// evolve rewrites a question to demand more complex reasoning while keeping it
// answerable from the same context (DeepEval-style data evolution).
func (sy Synthesizer) evolve(ctx context.Context, input string, group []string) (string, error) {
	var resp struct {
		Input string `json:"input"`
	}
	prompt := fmt.Sprintf(`Rewrite the QUESTION so it requires more complex reasoning (multi-step, comparative, or
inferential) while remaining fully answerable from the same CONTEXT and preserving its underlying answer.
Return STRICTLY JSON: {"input":"<rewritten question>"}

CONTEXT:
%s

QUESTION:
%s

JSON:`, joinContext(group), input)
	if err := callJSON(ctx, sy.Judge, prompt, &resp); err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Input), nil
}

// chunkText splits s into chunks of about size runes, breaking on whitespace so
// words stay intact. Returns the whole text as one chunk when it fits.
func chunkText(s string, size int) []string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return nil
	}
	var chunks []string
	var b strings.Builder
	for _, w := range fields {
		if b.Len() > 0 && b.Len()+1+len(w) > size {
			chunks = append(chunks, b.String())
			b.Reset()
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(w)
	}
	if b.Len() > 0 {
		chunks = append(chunks, b.String())
	}
	return chunks
}
