package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type fakeRemoteSkillPublicReader struct {
	file      service.RemoteSkillPublicFile
	err       error
	requested string
}

func TestRemoteSkillHandlerServesPercentEncodedUnicodePath(t *testing.T) {
	body := []byte("# Unicode path\n")
	reader := &fakeRemoteSkillPublicReader{file: service.RemoteSkillPublicFile{
		Body: body, ETag: `"unicode"`, ContentType: "text/markdown; charset=utf-8",
	}}
	router := newRemoteSkillPublicTestRouter(reader)
	name := "skills/sec-assessment-tooling/pentest-tools/src-hunter/references/payloader/by-category/web/认证漏洞.md"
	parts := strings.Split(name, "/")
	for index := range parts {
		parts[index] = url.PathEscape(parts[index])
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/skills/security-research/current/"+strings.Join(parts, "/"), nil)
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, name, reader.requested)
	require.Equal(t, body, response.Body.Bytes())
}

func (f *fakeRemoteSkillPublicReader) LoadPublishedFile(_ context.Context, name string) (service.RemoteSkillPublicFile, error) {
	f.requested = name
	return f.file, f.err
}

func newRemoteSkillPublicTestRouter(reader RemoteSkillPublicReader) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewRemoteSkillHandler(reader)
	router.Any("/skills/security-research/current", handler.Serve)
	router.Any("/skills/security-research/current/*path", handler.Serve)
	return router
}

func TestRemoteSkillHandlerServesGETAndHEADWithStableHeaders(t *testing.T) {
	body := []byte("# Skill\n")
	reader := &fakeRemoteSkillPublicReader{file: service.RemoteSkillPublicFile{
		Body: body, ETag: `"abc"`, ContentType: "text/markdown; charset=utf-8",
	}}
	router := newRemoteSkillPublicTestRouter(reader)

	get := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/skills/security-research/current/SKILL.md", nil)
	router.ServeHTTP(get, request)
	require.Equal(t, http.StatusOK, get.Code)
	require.Equal(t, "SKILL.md", reader.requested)
	require.Equal(t, body, get.Body.Bytes())
	require.Equal(t, `"abc"`, get.Header().Get("ETag"))
	require.Equal(t, "public, max-age=300", get.Header().Get("Cache-Control"))
	require.Equal(t, "nosniff", get.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "text/markdown; charset=utf-8", get.Header().Get("Content-Type"))

	head := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodHead, "/skills/security-research/current/SKILL.md", nil)
	router.ServeHTTP(head, request)
	require.Equal(t, http.StatusOK, head.Code)
	require.Empty(t, head.Body.Bytes())
	require.Equal(t, len(body), int(head.Result().ContentLength))

	notModified := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/skills/security-research/current/SKILL.md", nil)
	request.Header.Set("If-None-Match", `"abc"`)
	router.ServeHTTP(notModified, request)
	require.Equal(t, http.StatusNotModified, notModified.Code)
	require.Empty(t, notModified.Body.Bytes())
}

func TestRemoteSkillHandlerRejectsOtherMethodsTraversalDirectoriesAndMissingFiles(t *testing.T) {
	reader := &fakeRemoteSkillPublicReader{file: service.RemoteSkillPublicFile{
		Body: []byte("ok"), ETag: `"abc"`, ContentType: "text/plain; charset=utf-8",
	}}
	router := newRemoteSkillPublicTestRouter(reader)

	for _, tc := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodPost, "/skills/security-research/current/SKILL.md", http.StatusMethodNotAllowed},
		{http.MethodGet, "/skills/security-research/current", http.StatusNotFound},
		{http.MethodGet, "/skills/security-research/current/", http.StatusNotFound},
		{http.MethodGet, "/skills/security-research/current/references/", http.StatusNotFound},
		{http.MethodGet, "/skills/security-research/current/%2e%2e/SKILL.md", http.StatusNotFound},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(tc.method, tc.path, nil)
			router.ServeHTTP(response, request)
			require.Equal(t, tc.status, response.Code)
			require.Empty(t, response.Header().Get("Location"))
		})
	}

	reader.err = service.ErrRemoteSkillPublicFileNotFound
	missing := httptest.NewRecorder()
	router.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/skills/security-research/current/missing.md", nil))
	require.Equal(t, http.StatusNotFound, missing.Code)
}
