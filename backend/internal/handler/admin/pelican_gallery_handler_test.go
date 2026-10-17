//go:build unit

package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPelicanGalleryReadAndSVGFailurePreview(t *testing.T) {
	gin.SetMode(gin.TestMode)
	legacy, base := newPelicanHandlerFixture(t)
	repo := &pelicanSummaryHandlerRepo{base}
	runner := service.NewPelicanTestRunner(repo, nil, nil)
	t.Cleanup(runner.Stop)
	h := NewPelicanTestHandler(nil, runner, legacy.preview)
	router := gin.New()
	router.GET("/results", h.Gallery)
	router.GET("/api/v1/pelican-tests/preview/:cap/scene.svg", h.ServeSVG)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/results?page=1&page_size=24", nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.NotContains(t, response.Body.String(), "raw_answer")
	require.Zero(t, base.creates)
	require.Zero(t, base.reads)
	require.Zero(t, base.writes)
	base.result.Status = "failed"
	base.result.SVG = `<svg xmlns="http://www.w3.org/2000/svg"><text>saved work</text></svg>`
	_, base.result.HTML = service.ExtractPelicanHTML(base.result.SVG)
	preview := h.preview.DecorateResult(base.result)
	require.Contains(t, preview.PreviewURL, "scene.svg")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", preview.PreviewURL, nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, base.result.SVG, response.Body.String())
	require.Contains(t, response.Header().Get("Content-Type"), "image/svg+xml")
	require.Contains(t, response.Header().Get("Content-Security-Policy"), "script-src 'none'")
}
