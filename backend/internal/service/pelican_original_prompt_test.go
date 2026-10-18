//go:build unit

package service

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPelicanOriginalPromptAndFormats(t *testing.T) {
	const original = "创建一个HTML，内容是SVG绘制一个鹈鹕骑自行车的2D动画，你不需要任何测试"
	require.Equal(t, original, PelicanPrompt)
	frontend, err := os.ReadFile("../../../frontend/src/api/admin/pelicanTests.ts")
	require.NoError(t, err)
	require.Contains(t, string(frontend), "export const PELICAN_PROMPT = '"+original+"'")
	for _, source := range []string{
		"<svg xmlns=\"http://www.w3.org/2000/svg\"><circle r=\"20\"/></svg>",
		"<!doctype html><html><head><style>@keyframes pedal{to{transform:rotate(360deg)}}svg{animation:pedal 2s infinite}</style></head><body><svg/></body></html>",
		"<html><body><svg id=\"work\"></svg><script>requestAnimationFrame(function frame(){requestAnimationFrame(frame)})</script></body></html>",
	} {
		t.Run(source[:12], func(t *testing.T) {
			g := pelicanParallelEvaluator{call: func(_ context.Context, _ int64, _ string, input PelicanModelInput) (*PelicanResult, error) {
				if len(input.Images) > 0 {
					return &PelicanResult{Status: "failed", Error: "review request failed", RawResponse: "REVIEW-RAW"}, nil
				}
				require.Equal(t, original, input.Text)
				return &PelicanResult{Status: "complete", RawAnswer: source, RawResponse: "GEN-RAW"}, nil
			}}
			repo := newPelicanMemoryTestRepo()
			runner := newPelicanTestRunner(repo, g, nil)
			t.Cleanup(runner.Stop)
			runner.renderer = pelicanFixedRenderer{}
			e, err := newPelicanEvaluation(PelicanTestConfig{TestItems: []string{"pelican"}, Scene: PelicanScenes[0]}, PelicanTestTarget{Scene: PelicanScenes[1]})
			require.NoError(t, err)
			result := &PelicanTestResult{ID: "work", TaskID: "task", AccountID: 1, ModelID: "model", Evaluation: e, ExpiresAt: time.Now().Add(time.Hour)}
			repo.resultByID[result.ID] = result
			runner.runEvaluation(context.Background(), result, time.Minute, nil)
			require.Equal(t, "passed", e.Generation)
			require.Equal(t, "error", e.Review)
			require.Equal(t, "failed", result.Status)
			require.Empty(t, e.Scene)
			require.Empty(t, e.Nonce)
			require.Empty(t, result.SVG)
			require.Equal(t, source, result.RawHTML)
			require.Contains(t, result.HTML, source)
			require.Equal(t, original, result.Evidence["generating"].Prompt)
			summary := pelicanResultSummary(result, "")
			require.True(t, summary.HasPreview)
			require.False(t, summary.SVGPreview)
			preview := NewPelicanPreviewService(repo)
			t.Cleanup(preview.Stop)
			preview.DecorateSummary(summary)
			require.True(t, strings.HasSuffix(summary.PreviewURL, "/index.html"))
			cap := strings.TrimSuffix(strings.TrimPrefix(summary.PreviewURL, "/api/v1/pelican-tests/preview/"), "/index.html")
			html, err := preview.Resolve(context.Background(), cap)
			require.NoError(t, err)
			require.Equal(t, result.HTML, html)
			result.ExpiresAt = time.Now().Add(-time.Second)
			_, err = preview.Resolve(context.Background(), cap)
			require.ErrorIs(t, err, ErrPelicanPreviewUnavailable)
		})
	}
}

func TestPelicanOriginalPromptSceneReplayCompatibility(t *testing.T) {
	repo := newPelicanMemoryTestRepo()
	runner := newPelicanTestRunner(repo, nil, nil)
	t.Cleanup(runner.Stop)
	request := PelicanTestRequest{ClientTaskID: pelicanV7(t), PelicanTestConfig: PelicanTestConfig{TestItems: []string{"pelican"}, Scene: PelicanScenes[0]}, Targets: []PelicanTestTarget{{AccountID: 1, ModelID: "a", Scene: PelicanScenes[1]}}}
	normalized, err := normalizePelicanTestRequest(request, time.Now())
	require.NoError(t, err)
	task, _, err := runner.Create(context.Background(), 1, request)
	require.NoError(t, err)
	require.Equal(t, pelicanTestRequestHash(normalized), task.RequestHash)
	require.Equal(t, PelicanPrompt, task.Prompt)
	require.Empty(t, task.TestConfig.Scene)
	require.Empty(t, task.Results[0].Evaluation.Scene)
	repo.tasks[task.ID].Prompt = "historical prompt"
	task.Results[0].Evaluation.Scene = "historical scene"
	replayed, existing, err := runner.Create(context.Background(), 1, request)
	require.NoError(t, err)
	require.True(t, existing)
	require.Equal(t, "historical prompt", replayed.Prompt)
	require.Equal(t, "historical scene", replayed.Results[0].Evaluation.Scene)
}

func TestPelicanOriginalPromptScriptDetection(t *testing.T) {
	for _, source := range []string{"<html><script>animate()</script></html>", "<svg onload=\"animate()\"></svg>", "<HTML><SCRIPT src=\"external\"></SCRIPT></HTML>"} {
		require.True(t, pelicanWorkHasScript(source))
	}
	for _, source := range []string{"<html><style>@keyframes animate{}</style><svg/></html>", "<svg><animate dur=\"2s\"/></svg>", "<html><!-- <script>comment</script> --></html>"} {
		require.False(t, pelicanWorkHasScript(source))
	}
}
