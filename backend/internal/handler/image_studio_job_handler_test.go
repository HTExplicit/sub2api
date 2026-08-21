package handler

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type imageStudioHandlerServiceStub struct {
	eligible  []service.ImageStudioEligibleKey
	created   *service.ImageStudioJob
	create    service.ImageStudioCreateInput
	reference *service.ImageStudioUpload
	mask      *service.ImageStudioUpload
	userID    int64
	download  *service.ImageStudioArtifactDownload
}

func (s *imageStudioHandlerServiceStub) EligibleKeys(context.Context, int64) ([]service.ImageStudioEligibleKey, error) {
	return s.eligible, nil
}
func (s *imageStudioHandlerServiceStub) Create(_ context.Context, userID int64, input service.ImageStudioCreateInput, reference, mask *service.ImageStudioUpload) (*service.ImageStudioJob, error) {
	s.userID, s.create, s.reference, s.mask = userID, input, reference, mask
	return s.created, nil
}
func (s *imageStudioHandlerServiceStub) Get(context.Context, int64, int64) (*service.ImageStudioJob, error) {
	return &service.ImageStudioJob{ID: 41}, nil
}
func (s *imageStudioHandlerServiceStub) List(context.Context, int64, int, int) ([]service.ImageStudioJob, error) {
	return nil, nil
}
func (s *imageStudioHandlerServiceStub) ListItems(context.Context, int64, int64) ([]service.ImageStudioItem, error) {
	return nil, nil
}
func (s *imageStudioHandlerServiceStub) ListArtifacts(context.Context, int64, int64) ([]service.ImageStudioArtifact, error) {
	return nil, nil
}
func (s *imageStudioHandlerServiceStub) Cancel(context.Context, int64, int64) (*service.ImageStudioJob, error) {
	return &service.ImageStudioJob{ID: 41, Status: service.ImageStudioJobCanceled}, nil
}
func (s *imageStudioHandlerServiceStub) Retry(context.Context, int64, int64) (*service.ImageStudioJob, error) {
	return &service.ImageStudioJob{ID: 41, Status: service.ImageStudioJobPending}, nil
}
func (s *imageStudioHandlerServiceStub) OpenArtifact(context.Context, int64, int64, int64) (*service.ImageStudioArtifactDownload, error) {
	return s.download, nil
}

func imageStudioHandlerContext(method, target string, body io.Reader, contentType string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, target, body)
	if contentType != "" {
		c.Request.Header.Set("Content-Type", contentType)
	}
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})
	return c, recorder
}

func TestImageStudioJobHandlerEligibleKeysNeverReturnsSecret(t *testing.T) {
	t.Setenv(service.ImageStudioEnabledEnv, "true")
	stub := &imageStudioHandlerServiceStub{eligible: []service.ImageStudioEligibleKey{{
		APIKey: service.ImageStudioEligibleAPIKey{ID: 9, Name: "studio", GroupID: 10},
	}}}
	c, recorder := imageStudioHandlerContext(http.MethodGet, "/api/v1/image-studio/eligible-keys", nil, "")
	newImageStudioJobHandlerForTest(stub).EligibleKeys(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "server-only-secret")
	require.NotContains(t, recorder.Body.String(), `"key"`)
}

func TestImageStudioJobHandlerCreateReturnsAcceptedAndUsesSessionOwner(t *testing.T) {
	t.Setenv(service.ImageStudioEnabledEnv, "true")
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("api_key_id", "9"))
	require.NoError(t, writer.WriteField("mode", "edit"))
	require.NoError(t, writer.WriteField("model", service.ImageStudioModelGeminiProImage))
	require.NoError(t, writer.WriteField("prompt", "replace sky"))
	require.NoError(t, writer.WriteField("count", "4"))
	part, err := writer.CreateFormFile("reference", "reference.png")
	require.NoError(t, err)
	_, err = part.Write(imageStudioExecutorPNG())
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	stub := &imageStudioHandlerServiceStub{created: &service.ImageStudioJob{ID: 41, Status: service.ImageStudioJobPending}}
	c, recorder := imageStudioHandlerContext(http.MethodPost, "/api/v1/image-studio/jobs", &body, writer.FormDataContentType())
	newImageStudioJobHandlerForTest(stub).Create(c)
	require.Equal(t, http.StatusAccepted, recorder.Code, recorder.Body.String())
	require.Equal(t, int64(7), stub.userID)
	require.Equal(t, int64(9), stub.create.APIKeyID)
	require.Equal(t, 4, stub.create.Count)
	require.NotNil(t, stub.reference)
	require.Nil(t, stub.mask)
}

func TestImageStudioJobHandlerArtifactUsesSessionOwner(t *testing.T) {
	t.Setenv(service.ImageStudioEnabledEnv, "true")
	stub := &imageStudioHandlerServiceStub{download: &service.ImageStudioArtifactDownload{
		Artifact: &service.ImageStudioArtifact{ID: 52, ContentType: "image/png", ByteSize: int64(len(imageStudioExecutorPNG()))},
		Reader:   io.NopCloser(bytes.NewReader(imageStudioExecutorPNG())),
	}}
	c, recorder := imageStudioHandlerContext(http.MethodGet, "/api/v1/image-studio/jobs/41/artifacts/52", nil, "")
	c.Params = gin.Params{{Key: "id", Value: "41"}, {Key: "artifact_id", Value: "52"}}
	newImageStudioJobHandlerForTest(stub).Artifact(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
	require.Equal(t, imageStudioExecutorPNG(), recorder.Body.Bytes())
}

func TestImageStudioJobHandlerRequiresSessionOwner(t *testing.T) {
	t.Setenv(service.ImageStudioEnabledEnv, "true")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/image-studio/eligible-keys", nil)
	newImageStudioJobHandlerForTest(&imageStudioHandlerServiceStub{}).EligibleKeys(c)
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}
