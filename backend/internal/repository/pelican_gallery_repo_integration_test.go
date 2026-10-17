//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPelicanGalleryIntegration(t *testing.T) {
	ctx := context.Background()
	db := pelicanPrivateIntegrationDB(t, ctx)
	repo := &pelicanTestRepository{db: db}
	first := pelicanIntegrationTask(t, 42)
	first.TestConfig = service.PelicanTestConfig{TestItems: []string{"pelican", "candy"}}
	second := pelicanIntegrationTask(t, 43)
	for _, task := range []*service.PelicanTestTask{first, second} {
		_, _, err := repo.Create(ctx, task)
		require.NoError(t, err)
		result := task.Results[0]
		result.Status = "failed"
		now := time.Now().UTC()
		result.StartedAt = &now
		result.SVG = `<svg xmlns="http://www.w3.org/2000/svg"><text>saved</text></svg>`
		result.HTML = "<html>saved preview</html>"
		result.RawResponse = "GENERATION-CANARY"
		result.Evaluation = &service.PelicanEvaluation{TestItems: []string{"pelican", "candy"}, Scene: "雪山谷地", Nonce: "ABC123", Generation: "passed", Candy: "passed", Review: "error", Verdict: "error"}
		result.Evidence = map[string]*service.PelicanStageEvidence{"reviewing": {RawResponse: []byte{0xff, 0, 1}, Error: []byte("REVIEW-CANARY")}}
		result.ReviewImage = []byte("sheet")
		require.NoError(t, repo.SaveResult(ctx, result))
	}
	page, err := repo.ListTaskResults(ctx, "", service.PelicanResultFilter{Page: 1, Size: 1, Scene: "雪山谷地", Verdict: "error"})
	require.NoError(t, err)
	require.EqualValues(t, 2, page.Total)
	require.Len(t, page.Items, 1)
	require.Equal(t, second.ID, page.Items[0].TaskID)
	require.True(t, page.Items[0].HasPreview)
	encoded, err := json.Marshal(page)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "CANARY")
	require.NotContains(t, string(encoded), "raw_response")
	require.NotContains(t, string(encoded), "review_image")
	filtered, err := repo.ListTaskResults(ctx, "", service.PelicanResultFilter{AccountID: 42})
	require.NoError(t, err)
	require.EqualValues(t, 1, filtered.Total)
	full, err := repo.GetResult(ctx, first.Results[0].ID)
	require.NoError(t, err)
	require.Equal(t, []byte{0xff, 0, 1}, full.Evidence["reviewing"].RawResponse)
	preview, err := repo.GetPreviewResult(ctx, full.ID)
	require.NoError(t, err)
	require.Equal(t, full.SVG, preview.SVG)
	require.Empty(t, preview.RawResponse)
	require.Empty(t, preview.Evidence)
	snapshot, err := repo.GetTaskSnapshot(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, 1, snapshot.FailedCount)
	require.Equal(t, first.TestConfig, snapshot.TestConfig)
	require.NoError(t, repo.FinishTask(ctx, first.ID, "failed", "", time.Now()))
	require.NoError(t, repo.DeleteTask(ctx, first.ID))
	_, err = repo.GetPreviewResult(ctx, full.ID)
	require.Error(t, err)
	page, err = repo.ListTaskResults(ctx, "", service.PelicanResultFilter{})
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Total)
}
