//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPelicanCandyAndReview(t *testing.T) {
	for _, value := range []string{"21", "答案是 21 个。", "最少21个", "(21)", "21.5 不对，应为 21"} {
		require.True(t, pelicanCandyPasses(value), value)
	}
	for _, value := range []string{"121", "21.5", "-21", "+21", "a21", "21st", "21_", "20"} {
		require.False(t, pelicanCandyPasses(value), value)
	}
	for _, tc := range []struct{ value, status string }{{`{"checks":{"pelican":true,"bicycle":true,"riding":true,"motion":true}}`, "passed"}, {`{"checks":{"pelican":true,"bicycle":true,"riding":false,"motion":null}}`, "invalid"}, {`{"checks":{"pelican":true,"bicycle":true,"riding":true,"motion":null}}`, "uncertain"}} {
		_, status, err := parsePelicanReview(tc.value)
		require.NoError(t, err)
		require.Equal(t, tc.status, status)
	}
	_, _, err := parsePelicanReview(`{"checks":{"pelican":true}}`)
	require.Error(t, err)

}

type pelicanEvaluationGenerator struct {
	calls     []PelicanModelInput
	image     bool
	budgets   []time.Duration
	candy     string
	review    string
	failCandy bool
	cancel    context.CancelFunc
}

func (g *pelicanEvaluationGenerator) GeneratePelican(context.Context, int64, string, string) (*PelicanResult, error) {
	panic("legacy generator called")
}
func (g *pelicanEvaluationGenerator) PelicanImageCapability(context.Context, int64, string) (bool, error) {
	return g.image, nil
}
func (g *pelicanEvaluationGenerator) GeneratePelicanInput(ctx context.Context, _ int64, _, _ string, input PelicanModelInput) (*PelicanResult, error) {
	g.calls = append(g.calls, input)
	g.budgets = append(g.budgets, pelicanExecutionFromContext(ctx).budget)
	if input.Text == PelicanCandyPrompt {
		if g.cancel != nil {
			g.cancel()
		}
		if g.failCandy {
			return &PelicanResult{Status: "failed", RawResponse: "upstream failure", Error: "full error"}, errors.New("full error")
		}
		return &PelicanResult{Status: "complete", RawAnswer: g.candy}, nil
	}
	if len(input.Images) > 0 {
		return &PelicanResult{Status: "complete", RawAnswer: g.review}, nil
	}
	return &PelicanResult{Status: "complete", RawAnswer: `<html><body><svg><circle r="30"/></svg><script>requestAnimationFrame(function animate(){requestAnimationFrame(animate)})</script></body></html>`}, nil
}

type pelicanFixedRenderer struct{ err error }

func (r pelicanFixedRenderer) Render(context.Context, string) ([]PelicanImage, []byte, error) {
	return []PelicanImage{{PNG: []byte("frame"), Time: 0}, {PNG: []byte("frame"), Time: .46}, {PNG: []byte("frame"), Time: 1.02}, {PNG: []byte("frame"), Time: 1.58}}, []byte("sheet"), r.err
}

func TestPelicanEvaluationStagesKeepIndependentResults(t *testing.T) {
	for _, tc := range []struct {
		name              string
		items             []string
		vision, failCandy bool
		review, want      string
		renderErr         error
		calls             int
	}{
		{"both", []string{"pelican", "candy"}, true, false, `{"checks":{"pelican":true,"bicycle":true,"riding":true,"motion":true}}`, "passed", nil, 3},
		{"candy fails but SVG survives", []string{"pelican", "candy"}, true, true, `{"checks":{"pelican":true,"bicycle":true,"riding":true,"motion":true}}`, "error", nil, 3},
		{"review refuses", []string{"pelican"}, true, false, `{"checks":{"pelican":true,"bicycle":true,"riding":false,"motion":true}}`, "invalid", nil, 2},
		{"uncertain", []string{"pelican"}, true, false, `{"checks":{"pelican":true,"bicycle":true,"riding":null,"motion":true}}`, "uncertain", nil, 2},
		{"no image support", []string{"pelican"}, false, false, "", "not_reviewed", nil, 1},
		{"render error", []string{"pelican"}, true, false, "", "error", errors.New("render failed"), 1},
		{"candy only", []string{"candy"}, false, false, "", "passed", nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &pelicanEvaluationGenerator{image: tc.vision, candy: "最少21个", review: tc.review, failCandy: tc.failCandy}
			repo := newPelicanMemoryTestRepo()
			r := newPelicanTestRunner(repo, g, nil)
			defer r.Stop()
			r.renderer = pelicanFixedRenderer{tc.renderErr}
			e, err := newPelicanEvaluation(PelicanTestConfig{TestItems: tc.items}, PelicanTestTarget{})
			require.NoError(t, err)
			result := &PelicanTestResult{ID: "result", TaskID: "task", AccountID: 1, ModelID: "model", Evaluation: e, ExpiresAt: time.Now().Add(time.Hour)}
			persistedWork := false
			repo.onSave = func(value *PelicanTestResult) error {
				if value.Status == "running" && value.HTML != "" {
					persistedWork = true
				}
				return nil
			}
			r.runEvaluation(context.Background(), result, time.Minute, nil)
			require.Equal(t, tc.want, e.Verdict)
			require.Len(t, g.calls, tc.calls)
			if tc.items[0] == "pelican" {
				require.NotEmpty(t, result.HTML)
				require.True(t, persistedWork)
				require.Empty(t, result.SVG)
				require.Empty(t, e.Scene)
				require.Empty(t, e.Nonce)
				require.Equal(t, map[string]bool{"document": true}, e.Checks)
				require.Equal(t, PelicanPrompt, result.Evidence["generating"].Prompt)
				require.True(t, pelicanResultSummary(result, "").HasPreview)
			}
			if tc.calls == 3 {
				require.Equal(t, []time.Duration{90 * time.Second, time.Minute, 180 * time.Second}, g.budgets)
				require.Empty(t, g.calls[0].Images)
				require.Empty(t, g.calls[1].Images)
				require.Len(t, g.calls[2].Images, 4)
			}
		})
	}
}

func TestPelicanEvaluationCancellationStopsLaterStages(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g := &pelicanEvaluationGenerator{candy: "21", cancel: cancel}
	r := newPelicanTestRunner(newPelicanMemoryTestRepo(), g, nil)
	defer r.Stop()
	e, err := newPelicanEvaluation(PelicanTestConfig{TestItems: []string{"pelican", "candy"}}, PelicanTestTarget{})
	require.NoError(t, err)
	result := &PelicanTestResult{AccountID: 1, Evaluation: e}
	r.runEvaluation(ctx, result, time.Minute, nil)
	require.Len(t, g.calls, 1)
	require.Equal(t, "cancelled", result.Status)
	require.Empty(t, result.SVG)
}
