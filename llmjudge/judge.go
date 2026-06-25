// Package llmjudge adapts an agent-go LLM client into an evalgo.JudgeFunc.
// It lives apart from the core evalgo package so deterministic-only users don't
// pull in agent-go. It depends only on agent-go's published, stable API.
package llmjudge

import (
	"context"
	"fmt"
	"os"

	evalgo "github.com/liliang-cn/eval-go"

	"github.com/liliang-cn/agent-go/v2/pkg/domain"
	"github.com/liliang-cn/agent-go/v2/pkg/llm"
	"github.com/liliang-cn/agent-go/v2/pkg/providers"
)

// New wraps an agent-go *llm.Service as a judge.
func New(svc *llm.Service) evalgo.JudgeFunc {
	return func(ctx context.Context, prompt string) (string, error) {
		return svc.Generate(ctx, prompt, &domain.GenerationOptions{})
	}
}

// FromEnv builds a judge against any OpenAI-compatible endpoint from
// LLM_BASE_URL / LLM_API_KEY / LLM_MODEL. Returns an error if creds are missing.
func FromEnv() (evalgo.JudgeFunc, error) {
	base, key, model := os.Getenv("LLM_BASE_URL"), os.Getenv("LLM_API_KEY"), os.Getenv("LLM_MODEL")
	if base == "" || key == "" || model == "" {
		return nil, fmt.Errorf("set LLM_BASE_URL, LLM_API_KEY and LLM_MODEL")
	}
	gen, err := providers.NewOpenAILLMProvider(&domain.OpenAIProviderConfig{
		BaseURL:  base,
		APIKey:   key,
		LLMModel: model,
	})
	if err != nil {
		return nil, err
	}
	return New(llm.NewService(gen)), nil
}
