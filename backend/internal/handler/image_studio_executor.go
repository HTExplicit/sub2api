package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type imageStudioImagesInvoker interface {
	Images(*gin.Context)
}

type imageStudioSubscriptionFinder interface {
	GetActiveSubscription(context.Context, int64, int64) (*service.UserSubscription, error)
}

type ImageStudioGatewayExecutor struct {
	images        imageStudioImagesInvoker
	subscriptions imageStudioSubscriptionFinder
}

func NewImageStudioGatewayExecutor(images imageStudioImagesInvoker, subscriptions *service.SubscriptionService) *ImageStudioGatewayExecutor {
	return &ImageStudioGatewayExecutor{images: images, subscriptions: subscriptions}
}

func newImageStudioGatewayExecutorForTest(images imageStudioImagesInvoker, subscriptions imageStudioSubscriptionFinder) *ImageStudioGatewayExecutor {
	return &ImageStudioGatewayExecutor{images: images, subscriptions: subscriptions}
}

func (e *ImageStudioGatewayExecutor) Execute(ctx context.Context, request service.ImageStudioExecutionRequest) (*service.ImageStudioExecutionResult, error) {
	if e == nil || e.images == nil || request.APIKey == nil || request.APIKey.User == nil {
		return nil, errors.New("image gateway is unavailable")
	}
	body, contentType, path, err := buildImageStudioGatewayRequest(request)
	if err != nil {
		return nil, err
	}
	httpRequest := httptest.NewRequest(http.MethodPost, path, body).WithContext(ctx)
	httpRequest.Header.Set("Content-Type", contentType)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httpRequest
	c.Set(string(middleware2.ContextKeyAPIKey), request.APIKey)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{
		UserID: request.APIKey.UserID, Concurrency: request.APIKey.User.Concurrency,
	})
	c.Set(string(middleware2.ContextKeyUserRole), request.APIKey.User.Role)
	if request.APIKey.Group != nil && request.APIKey.Group.IsSubscriptionType() {
		if e.subscriptions == nil {
			return nil, errors.New("image subscription is unavailable")
		}
		subscription, lookupErr := e.subscriptions.GetActiveSubscription(ctx, request.APIKey.UserID, request.APIKey.Group.ID)
		if lookupErr != nil || subscription == nil {
			return nil, errors.New("image subscription is unavailable")
		}
		c.Set(string(middleware2.ContextKeySubscription), subscription)
	}
	e.images.Images(c)
	if recorder.Code != http.StatusOK {
		return nil, errors.New("image gateway request failed")
	}
	return decodeImageStudioGatewayResponse(recorder.Body.Bytes())
}

func buildImageStudioGatewayRequest(request service.ImageStudioExecutionRequest) (*bytes.Reader, string, string, error) {
	if request.Job.Mode == service.ImageStudioModeEdit {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		fields := map[string]string{
			"model": request.Job.Model, "prompt": request.Job.Prompt, "n": "1",
			"response_format": "b64_json", "size": request.Job.Size, "quality": request.Job.Quality,
		}
		for name, value := range fields {
			if value != "" {
				if err := writer.WriteField(name, value); err != nil {
					return nil, "", "", errors.New("build image edit request")
				}
			}
		}
		if err := writeImageStudioMultipartFile(writer, "image", "reference", request.ReferenceContentType, request.Reference); err != nil {
			return nil, "", "", err
		}
		if len(request.Mask) > 0 {
			if err := writeImageStudioMultipartFile(writer, "mask", "mask", request.MaskContentType, request.Mask); err != nil {
				return nil, "", "", err
			}
		}
		if err := writer.Close(); err != nil {
			return nil, "", "", errors.New("build image edit request")
		}
		return bytes.NewReader(body.Bytes()), writer.FormDataContentType(), "/v1/images/edits", nil
	}
	payload := map[string]any{
		"model": request.Job.Model, "prompt": request.Job.Prompt, "n": 1,
		"response_format": "b64_json", "size": request.Job.Size, "quality": request.Job.Quality,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, "", "", errors.New("build image generation request")
	}
	return bytes.NewReader(body), "application/json", "/v1/images/generations", nil
}

func writeImageStudioMultipartFile(writer *multipart.Writer, field, baseName, contentType string, data []byte) error {
	if len(data) == 0 {
		return errors.New("image input is unavailable")
	}
	extension := ".png"
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/jpeg":
		extension = ".jpg"
	case "image/webp":
		extension = ".webp"
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, field, baseName+extension))
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return errors.New("build image edit request")
	}
	if _, err = part.Write(data); err != nil {
		return errors.New("build image edit request")
	}
	return nil
}

func decodeImageStudioGatewayResponse(body []byte) (*service.ImageStudioExecutionResult, error) {
	var payload struct {
		Data []struct {
			B64JSON       string `json:"b64_json"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || len(payload.Data) != 1 || payload.Data[0].B64JSON == "" {
		return nil, errors.New("image gateway returned an invalid result")
	}
	encoded := payload.Data[0].B64JSON
	if base64.StdEncoding.DecodedLen(len(encoded)) > service.ImageStudioMaxImageBytes {
		return nil, errors.New("image gateway returned an oversized result")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) > service.ImageStudioMaxImageBytes {
		return nil, errors.New("image gateway returned an invalid result")
	}
	contentType := imageStudioContentType(data)
	if contentType == "" {
		return nil, errors.New("image gateway returned an invalid image")
	}
	return &service.ImageStudioExecutionResult{
		Data: data, ContentType: contentType, RevisedPrompt: strings.TrimSpace(payload.Data[0].RevisedPrompt),
	}, nil
}

func imageStudioContentType(data []byte) string {
	switch {
	case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png"
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return "image/jpeg"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	default:
		return ""
	}
}
