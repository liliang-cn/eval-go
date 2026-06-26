package evalgo

import (
	"fmt"
	"sort"
	"strings"
)

// MetricSpec configures the metrics built by BuildMetrics.
type MetricSpec struct {
	Judge     JudgeFunc // required for semantic metrics; may be nil for deterministic-only
	Threshold float64   // pass threshold for relevancy / context_precision / rubric (default 0.5)
}

// metricFactory builds a metric; needsJudge marks semantic metrics.
type metricFactory struct {
	needsJudge bool
	build      func(MetricSpec) Metric
}

var registry = map[string]metricFactory{
	"nonempty":          {false, func(MetricSpec) Metric { return NonEmpty() }},
	"citation":          {false, func(MetricSpec) Metric { return CitationPresent() }},
	"valid_json":        {false, func(MetricSpec) Metric { return ValidJSON() }},
	"exact_match":       {false, func(MetricSpec) Metric { return ExactMatch() }},
	"faithfulness":      {true, func(s MetricSpec) Metric { return Faithfulness(s.Judge) }},
	"answer_relevancy":  {true, func(s MetricSpec) Metric { return AnswerRelevancy(s.Judge, s.Threshold) }},
	"context_precision": {true, func(s MetricSpec) Metric { return ContextualPrecision(s.Judge, s.Threshold) }},
	"rubric":            {true, func(s MetricSpec) Metric { return RubricJudge(s.Judge, s.Threshold) }},

	// agent metrics (evaluate a recorded agent run on the Sample)
	"tool_correctness":     {false, func(MetricSpec) Metric { return ToolCorrectness() }},
	"argument_correctness": {true, func(s MetricSpec) Metric { return ArgumentCorrectness(s.Judge, s.Threshold) }},
	"task_completion":      {true, func(s MetricSpec) Metric { return TaskCompletion(s.Judge, s.Threshold) }},
	"step_efficiency":      {true, func(s MetricSpec) Metric { return StepEfficiency(s.Judge, s.Threshold) }},
	"plan_quality":         {true, func(s MetricSpec) Metric { return PlanQuality(s.Judge, s.Threshold) }},
	"plan_adherence":       {true, func(s MetricSpec) Metric { return PlanAdherence(s.Judge, s.Threshold) }},

	// RAG retrieval-quality metrics
	"contextual_recall":    {true, func(s MetricSpec) Metric { return ContextualRecall(s.Judge, s.Threshold) }},
	"contextual_relevancy": {true, func(s MetricSpec) Metric { return ContextualRelevancy(s.Judge, s.Threshold) }},

	// safety / quality metrics
	"hallucination": {true, func(s MetricSpec) Metric { return Hallucination(s.Judge, s.Threshold) }},
	"bias":          {true, func(s MetricSpec) Metric { return Bias(s.Judge, s.Threshold) }},
	"toxicity":      {true, func(s MetricSpec) Metric { return Toxicity(s.Judge, s.Threshold) }},
	"pii_leakage":   {true, func(s MetricSpec) Metric { return PIILeakage(s.Judge) }},
	"summarization": {true, func(s MetricSpec) Metric { return Summarization(s.Judge, s.Threshold) }},

	// multi-turn conversational metrics
	"conversation_completeness": {true, func(s MetricSpec) Metric { return ConversationCompleteness(s.Judge, s.Threshold) }},
	"knowledge_retention":       {true, func(s MetricSpec) Metric { return KnowledgeRetention(s.Judge, s.Threshold) }},
	"conversation_relevancy":    {true, func(s MetricSpec) Metric { return ConversationRelevancy(s.Judge, s.Threshold) }},
	"role_adherence":            {true, func(s MetricSpec) Metric { return RoleAdherence(s.Judge, s.Threshold) }},

	// red-team / safety-probe metrics
	"attack_resistance": {true, func(s MetricSpec) Metric { return AttackResistance(s.Judge, s.Threshold) }},
	"refusal":           {false, func(MetricSpec) Metric { return RefusalPresent() }},
}

// RegisteredMetrics returns the metric names known to BuildMetrics, sorted.
func RegisteredMetrics() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// BuildMetrics resolves metric names into Metrics. It errors on an unknown name,
// or on a semantic metric requested without a judge — so misconfiguration fails
// fast instead of silently skipping evaluation.
func BuildMetrics(names []string, spec MetricSpec) ([]Metric, error) {
	if spec.Threshold == 0 {
		spec.Threshold = 0.5
	}
	out := make([]Metric, 0, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		f, ok := registry[name]
		if !ok {
			return nil, fmt.Errorf("unknown metric %q (known: %s)", name, strings.Join(RegisteredMetrics(), ", "))
		}
		if f.needsJudge && spec.Judge == nil {
			return nil, fmt.Errorf("metric %q requires a judge (configure an LLM judge)", name)
		}
		out = append(out, f.build(spec))
	}
	return out, nil
}
