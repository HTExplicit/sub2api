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
	"strconv"
	"strings"
	"sync"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
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
	ops           *service.OpsService

	routerOnce sync.Once
	router     *gin.Engine
}

func NewImageStudioGatewayExecutor(images imageStudioImagesInvoker, subscriptions *service.SubscriptionService, ops *service.OpsService) *ImageStudioGatewayExecutor {
	return &ImageStudioGatewayExecutor{images: images, subscriptions: subscriptions, ops: ops}
}

func newImageStudioGatewayExecutorForTest(images imageStudioImagesInvoker, subscriptions imageStudioSubscriptionFinder) *ImageStudioGatewayExecutor {
	return &ImageStudioGatewayExecutor{images: images, subscriptions: subscriptions}
}

// imageStudioGatewayCall carries one job item's authentication into the shared
// in-process router.
type imageStudioGatewayCall struct {
	apiKey       *service.APIKey
	subscription *service.UserSubscription
}

type imageStudioGatewayCallKey struct{}

// gatewayRouter runs Image Studio requests through the Ops error logger in
// front of the images handler, as the public /v1/images routes do, so failed
// generations leave an Ops error record.
func (e *ImageStudioGatewayExecutor) gatewayRouter() *gin.Engine {
	e.routerOnce.Do(func() {
		router := gin.New()
		router.Any("/*endpoint", OpsErrorLoggerMiddleware(e.ops), imageStudioGatewayAuth, e.images.Images)
		e.router = router
	})
	return e.router
}

// applyImageStudioUsageOrigin records the Image Studio submitter's address and
// User-Agent on the usage row of an in-process gateway request.
func applyImageStudioUsageOrigin(c *gin.Context, snapshot *openAIUsageSnapshot) {
	if c == nil || c.Request == nil || snapshot == nil {
		return
	}
	origin, ok := service.ImageStudioRequestOriginFromContext(c.Request.Context())
	if !ok {
		return
	}
	if userAgent := strings.TrimSpace(origin.UserAgent); userAgent != "" {
		snapshot.userAgent = userAgent
	}
	if clientIP := strings.TrimSpace(origin.ClientIP); clientIP != "" {
		snapshot.ipAddress = clientIP
	}
}

// applyImageStudioOpsOrigin records the Image Studio submitter's address and
// User-Agent on the Ops row of an in-process gateway request.
func applyImageStudioOpsOrigin(c *gin.Context, entry *service.OpsInsertErrorLogInput) {
	if c == nil || c.Request == nil || entry == nil {
		return
	}
	origin, ok := service.ImageStudioRequestOriginFromContext(c.Request.Context())
	if !ok {
		return
	}
	if userAgent := strings.TrimSpace(origin.UserAgent); userAgent != "" {
		entry.UserAgent = userAgent
	}
	if clientIP := strings.TrimSpace(origin.ClientIP); clientIP != "" {
		entry.ClientIP = &clientIP
	}
}

func imageStudioGatewayAuth(c *gin.Context) {
	call, _ := c.Request.Context().Value(imageStudioGatewayCallKey{}).(*imageStudioGatewayCall)
	if call == nil || call.apiKey == nil || call.apiKey.User == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	c.Set(string(middleware2.ContextKeyAPIKey), call.apiKey)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{
		UserID: call.apiKey.UserID, Concurrency: call.apiKey.User.Concurrency,
	})
	c.Set(string(middleware2.ContextKeyUserRole), call.apiKey.User.Role)
	if call.subscription != nil {
		c.Set(string(middleware2.ContextKeySubscription), call.subscription)
	}
	c.Next()
}

func (e *ImageStudioGatewayExecutor) Execute(ctx context.Context, request service.ImageStudioExecutionRequest) (*service.ImageStudioExecutionResult, error) {
	if e == nil || e.images == nil || request.APIKey == nil || request.APIKey.User == nil {
		return nil, errors.New("image gateway is unavailable")
	}
	plan, err := service.PlanImageStudioExecution(ctx, request)
	if err != nil {
		return nil, err
	}
	body, contentType, path, err := buildImageStudioGatewayRequest(request, plan)
	if err != nil {
		return nil, err
	}
	call := &imageStudioGatewayCall{apiKey: request.APIKey}
	if request.APIKey.Group != nil && request.APIKey.Group.IsSubscriptionType() {
		if e.subscriptions == nil {
			return nil, errors.New("image subscription is unavailable")
		}
		subscription, lookupErr := e.subscriptions.GetActiveSubscription(ctx, request.APIKey.UserID, request.APIKey.Group.ID)
		if lookupErr != nil {
			return nil, fmt.Errorf("image subscription is unavailable: %w", lookupErr)
		}
		if subscription == nil {
			return nil, errors.New("image subscription is unavailable")
		}
		call.subscription = subscription
	}
	// The correlation ID ties Ops and usage rows to this attempt of the job
	// item; it is also the usage billing dedupe key, so it is new per attempt.
	ctx = context.WithValue(ctx, ctxkey.ClientRequestID, service.NewImageStudioClientRequestID(request.Job.ID, request.Item.ID))
	ctx = context.WithValue(ctx, imageStudioGatewayCallKey{}, call)
	// The job's submitter is recorded on usage and Ops rows from the context.
	// It is not put on the request: the images handler forwards request
	// headers such as User-Agent to API-key upstreams.
	ctx = service.WithImageStudioRequestOrigin(ctx, request.Origin)
	httpRequest := httptest.NewRequest(http.MethodPost, path, body).WithContext(ctx)
	httpRequest.Header.Set("Content-Type", contentType)
	recorder := httptest.NewRecorder()
	e.gatewayRouter().ServeHTTP(recorder, httpRequest)
	if recorder.Code != http.StatusOK {
		return nil, &service.ImageStudioGatewayError{StatusCode: recorder.Code, Body: recorder.Body.String()}
	}
	return decodeImageStudioGatewayResponse(recorder.Body.Bytes())
}

func buildImageStudioGatewayRequest(request service.ImageStudioExecutionRequest, plan extensionv1.ImageStudioPlan) (*bytes.Reader, string, string, error) {
	if plan.Mode == string(service.ImageStudioModeEdit) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		fields := map[string]string{
			"model": plan.Model, "prompt": request.Job.Prompt, "n": strconv.Itoa(plan.OutputPerRequest),
			"response_format": plan.ResponseFormat, "size": plan.Size, "quality": plan.Quality,
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
		return bytes.NewReader(body.Bytes()), writer.FormDataContentType(), plan.Endpoint, nil
	}
	payload := map[string]any{
		"model": plan.Model, "prompt": request.Job.Prompt, "n": plan.OutputPerRequest,
		"response_format": plan.ResponseFormat, "size": plan.Size, "quality": plan.Quality,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, "", "", errors.New("build image generation request")
	}
	return bytes.NewReader(body), "application/json", plan.Endpoint, nil
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
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("image gateway returned a result that is not JSON: %w; response: %s", err, service.ImageStudioLogText(string(body)))
	}
	if len(payload.Data) != 1 || payload.Data[0].B64JSON == "" {
		return nil, fmt.Errorf("image gateway returned %d images instead of one base64 image; response: %s", len(payload.Data), service.ImageStudioLogText(string(body)))
	}
	encoded := payload.Data[0].B64JSON
	if size := base64.StdEncoding.DecodedLen(len(encoded)); size > service.ImageStudioMaxImageBytes {
		return nil, fmt.Errorf("image gateway returned an oversized image: about %d bytes, the limit is %d", size, service.ImageStudioMaxImageBytes)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("image gateway returned an image that is not valid base64: %w", err)
	}
	if len(data) > service.ImageStudioMaxImageBytes {
		return nil, fmt.Errorf("image gateway returned an oversized image: %d bytes, the limit is %d", len(data), service.ImageStudioMaxImageBytes)
	}
	contentType := imageStudioContentType(data)
	if contentType == "" {
		return nil, fmt.Errorf("image gateway returned %d bytes that are not a PNG, JPEG or WebP image (first bytes %x)", len(data), data[:min(len(data), 12)])
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
