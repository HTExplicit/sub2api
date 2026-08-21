package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type imageStudioImagesInvokerFunc func(*gin.Context)

func (f imageStudioImagesInvokerFunc) Images(c *gin.Context) { f(c) }

func imageStudioExecutorAPIKey() *service.APIKey {
	groupID := int64(31)
	return &service.APIKey{
		ID: 21, UserID: 11, Key: "server-only-secret", Status: service.StatusActive,
		GroupID: &groupID,
		Group: &service.Group{
			ID: groupID, Platform: service.PlatformCindy, WirePlatform: service.WirePlatformOpenAI,
			ProviderProfile: service.ProviderProfileCindyLaxaV1, Status: service.StatusActive, AllowImageGeneration: true,
		},
		User: &service.User{ID: 11, Status: service.StatusActive, Concurrency: 4},
	}
}

func imageStudioExecutorPNG() []byte {
	return []byte("\x89PNG\r\n\x1a\nfixture")
}

func TestImageStudioGatewayExecutorUsesNativeNOneForGenerate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var got map[string]any
	invoker := imageStudioImagesInvokerFunc(func(c *gin.Context) {
		require.Equal(t, "/v1/images/generations", c.Request.URL.Path)
		require.Contains(t, c.GetHeader("Content-Type"), "application/json")
		require.NoError(t, json.NewDecoder(c.Request.Body).Decode(&got))
		c.JSON(http.StatusOK, gin.H{"data": []gin.H{{"b64_json": base64.StdEncoding.EncodeToString(imageStudioExecutorPNG()), "revised_prompt": "revised"}}})
	})
	executor := newImageStudioGatewayExecutorForTest(invoker, nil)
	result, err := executor.Execute(context.Background(), service.ImageStudioExecutionRequest{
		Job:    service.ImageStudioJob{UserID: 11, Mode: service.ImageStudioModeGenerate, Model: service.ImageStudioModelGPTImage2, Prompt: "draw", Size: "1024x1024", Quality: "low", Count: 4},
		APIKey: imageStudioExecutorAPIKey(),
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, got["n"])
	require.Equal(t, service.ImageStudioModelGPTImage2, got["model"])
	require.Equal(t, "b64_json", got["response_format"])
	require.Equal(t, "image/png", result.ContentType)
	require.Equal(t, "revised", result.RevisedPrompt)
}

func TestImageStudioGatewayExecutorUsesNativeNOneForEdit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	invoker := imageStudioImagesInvokerFunc(func(c *gin.Context) {
		require.Equal(t, "/v1/images/edits", c.Request.URL.Path)
		require.NoError(t, c.Request.ParseMultipartForm(service.ImageStudioMaxImageBytes*2))
		require.Equal(t, "1", c.Request.FormValue("n"))
		require.Equal(t, service.ImageStudioModelGeminiProImage, c.Request.FormValue("model"))
		image, _, err := c.Request.FormFile("image")
		require.NoError(t, err)
		defer func() { require.NoError(t, image.Close()) }()
		gotImage, err := io.ReadAll(image)
		require.NoError(t, err)
		require.Equal(t, imageStudioExecutorPNG(), gotImage)
		mask, _, err := c.Request.FormFile("mask")
		require.NoError(t, err)
		defer func() { require.NoError(t, mask.Close()) }()
		gotMask, err := io.ReadAll(mask)
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(string(gotMask), "\x89PNG"))
		c.JSON(http.StatusOK, gin.H{"data": []gin.H{{"b64_json": base64.StdEncoding.EncodeToString(imageStudioExecutorPNG())}}})
	})
	executor := newImageStudioGatewayExecutorForTest(invoker, nil)
	result, err := executor.Execute(context.Background(), service.ImageStudioExecutionRequest{
		Job:    service.ImageStudioJob{UserID: 11, Mode: service.ImageStudioModeEdit, Model: service.ImageStudioModelGeminiProImage, Prompt: "edit", Size: "1024x1024", Quality: "low"},
		APIKey: imageStudioExecutorAPIKey(), Reference: imageStudioExecutorPNG(), ReferenceContentType: "image/png",
		Mask: imageStudioExecutorPNG(), MaskContentType: "image/png",
	})
	require.NoError(t, err)
	require.Equal(t, "image/png", result.ContentType)
}

func TestImageStudioGatewayExecutorDoesNotExposeUpstreamErrorBody(t *testing.T) {
	executor := newImageStudioGatewayExecutorForTest(imageStudioImagesInvokerFunc(func(c *gin.Context) {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": "private upstream body"}})
	}), nil)
	_, err := executor.Execute(context.Background(), service.ImageStudioExecutionRequest{
		Job:    service.ImageStudioJob{UserID: 11, Mode: service.ImageStudioModeGenerate, Model: service.ImageStudioModelGPTImage2, Prompt: "draw"},
		APIKey: imageStudioExecutorAPIKey(),
	})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private upstream body")
}

type imageStudioSubscriptionFinderStub struct {
	subscription *service.UserSubscription
}

func (s *imageStudioSubscriptionFinderStub) GetActiveSubscription(context.Context, int64, int64) (*service.UserSubscription, error) {
	return s.subscription, nil
}

func TestImageStudioGatewayExecutorPreservesSubscriptionBillingContext(t *testing.T) {
	key := imageStudioExecutorAPIKey()
	key.Group.SubscriptionType = service.SubscriptionTypeSubscription
	subscription := &service.UserSubscription{ID: 71, UserID: key.UserID, GroupID: key.Group.ID}
	executor := newImageStudioGatewayExecutorForTest(imageStudioImagesInvokerFunc(func(c *gin.Context) {
		got, ok := middleware2.GetSubscriptionFromContext(c)
		require.True(t, ok)
		require.Equal(t, subscription.ID, got.ID)
		c.JSON(http.StatusOK, gin.H{"data": []gin.H{{"b64_json": base64.StdEncoding.EncodeToString(imageStudioExecutorPNG())}}})
	}), &imageStudioSubscriptionFinderStub{subscription: subscription})

	_, err := executor.Execute(context.Background(), service.ImageStudioExecutionRequest{
		Job:    service.ImageStudioJob{UserID: key.UserID, Mode: service.ImageStudioModeGenerate, Model: service.ImageStudioModelGPTImage2, Prompt: "draw"},
		APIKey: key,
	})
	require.NoError(t, err)
}
