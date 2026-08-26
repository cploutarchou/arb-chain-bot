package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Fake is the deterministic advisor for tests and DB-less dev
// (ARB_AI_PROVIDER=fake): rule-based output from the prompt's embedded
// input, produced as raw JSON so it passes the exact same validation
// gate as a real provider.
type Fake struct{}

func (Fake) Name() string  { return "fake" }
func (Fake) Model() string { return "fake-advisor" }

func (Fake) Analyze(_ context.Context, prompt string) (string, error) {
	// The prompt embeds the typed input after "INPUT:"; recover it.
	idx := strings.LastIndex(prompt, "INPUT:")
	if idx < 0 {
		return "", fmt.Errorf("ai: fake advisor: prompt missing input block")
	}
	var in Input
	if err := json.Unmarshal([]byte(strings.TrimSpace(prompt[idx+len("INPUT:"):])), &in); err != nil {
		return "", fmt.Errorf("ai: fake advisor: %w", err)
	}

	resp := response{
		Summary: fmt.Sprintf("%s: %d evaluations, %d qualified, %d rejected; feed gaps %d.",
			in.Kind, in.Scanner.Evaluations, in.Scanner.Qualified, in.Scanner.Rejected, in.Feed.SeqGaps),
		Findings: []string{
			fmt.Sprintf("qualification rate %s", ratio(in.Scanner.Qualified, in.Scanner.Evaluations)),
		},
	}
	// Deterministic rule: zero qualifications over a busy window ⇒
	// suggest widening the evaluation TTL, clamped to the validation
	// bound; at the cap no recommendation is made (a no-op change is
	// noise, and an out-of-bounds one would fail validation — audit
	// P2-2 found the unclamped rule poisoned every analysis once the
	// operator had approved its way to the cap).
	const ttlCapMs = 10_000 // strategy validation bound for scanner.ttl_ms
	if in.Scanner.Evaluations >= 100 && in.Scanner.Qualified == 0 && in.Params.Scanner.TTLMs < ttlCapMs {
		next := in.Params.Scanner.TTLMs + 100
		if next > ttlCapMs {
			next = ttlCapMs
		}
		resp.Findings = append(resp.Findings, "no opportunities qualified despite active evaluation")
		resp.Recommendations = append(resp.Recommendations, recommendation{
			Parameter:        "scanner.ttl_ms",
			RecommendedValue: fmt.Sprintf("%d", next),
			Evidence: fmt.Sprintf("%d evaluations produced 0 qualified opportunities",
				in.Scanner.Evaluations),
			Reason:         "a longer opportunity TTL tolerates current latency between detection and simulation",
			Confidence:     "0.6",
			ExpectedEffect: "more opportunities survive to simulation",
			Risks:          "staler quotes at simulation time; monitor slippage",
		})
	}
	raw, err := json.Marshal(resp)
	return string(raw), err
}

func ratio(a, b int64) string {
	if b == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.2f%%", float64(a)*100/float64(b))
}
