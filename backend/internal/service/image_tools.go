package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

var imageToolsConfigOverride atomic.Pointer[extensionv1.ImageToolsConfig]

// currentImageToolsConfig returns the effective Image Studio and Responses image
// bridge switches: the stored administrator setting once loaded, otherwise the
// deploy-time rollout flags (the value the former image-tools plugin was seeded with).
func currentImageToolsConfig() (extensionv1.ImageToolsConfig, bool) {
	if config := imageToolsConfigOverride.Load(); config != nil {
		return *config, true
	}
	return LegacyImageToolsConfig(), true
}

// imageStudioStop is closed whenever Image Studio is switched off so running
// executions stop, as the former plugin's policy lease did.
var imageStudioStop = struct {
	mu sync.Mutex
	ch chan struct{}
}{ch: make(chan struct{})}

// ConfigureImageTools installs the effective switches (startup load, admin
// update, tests). A nil value falls back to the deploy-time rollout flags.
func ConfigureImageTools(config *extensionv1.ImageToolsConfig) {
	imageToolsConfigOverride.Store(config)
	if current, _ := currentImageToolsConfig(); !current.StudioEnabled {
		imageStudioStop.mu.Lock()
		close(imageStudioStop.ch)
		imageStudioStop.ch = make(chan struct{})
		imageStudioStop.mu.Unlock()
	}
}

// bindImageStudioEnabled returns a context that is canceled when Image Studio
// is switched off.
func bindImageStudioEnabled(ctx context.Context) (context.Context, context.CancelFunc) {
	imageStudioStop.mu.Lock()
	stop := imageStudioStop.ch
	imageStudioStop.mu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// observeNativeImageFacts lets tests inspect the facts the host derives for the
// native image policy. It is nil in production.
var observeNativeImageFacts func(extensionv1.ImageNativeRequest)

func ValidateCindyImageRequest(model string, request *OpenAIImagesRequest) error {
	return ValidateCindyImageRequestForAccount(context.Background(), nil, model, request)
}

func ValidateCindyImageRequestForAccount(ctx context.Context, account *Account, model string, request *OpenAIImagesRequest) error {
	if request == nil {
		return errors.New("image request is required")
	}
	for _, value := range []string{model, request.Size, request.Quality, request.ResponseFormat} {
		if len(value) > 256 {
			return errors.New("image control exceeds maximum length")
		}
	}
	snapshot, err := LoadCindyCatalogSnapshot(ctx, account)
	if err != nil {
		return err
	}
	capability, found := snapshot.Capability(model)
	endpoint := CindyEndpointImagesGenerate
	if request.IsEdits() {
		endpoint = CindyEndpointImagesEdit
	}
	verified := found && capability.PublicModel && snapshot.Config.CatalogEnabled && snapshot.Images.StudioEnabled && slices.Contains(capability.VerifiedEndpoints, endpoint)
	facts := extensionv1.ImageNativeRequest{Model: strings.TrimSpace(model), Capability: capability, Verified: verified, Editing: request.IsEdits(), Stream: request.Stream, Count: request.N, Size: strings.TrimSpace(request.Size), Quality: strings.TrimSpace(request.Quality), ResponseFormat: strings.TrimSpace(request.ResponseFormat), HasReference: request.InputImageCount() > 0, HasMask: request.HasMask,
		UnverifiedControls: request.Background != "" || request.OutputFormat != "" || request.Moderation != "" || request.InputFidelity != "" || request.Style != "" || request.OutputCompression != nil || request.PartialImages != nil}
	if observeNativeImageFacts != nil {
		observeNativeImageFacts(facts)
	}
	return validateNativeImageRequest(facts)
}

func EnsureImageStudioAvailable(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return newImageStudioError(503, "studio_unavailable", "Image Studio is unavailable")
	}
	config, _ := currentImageToolsConfig()
	if !config.StudioEnabled {
		return newImageStudioError(404, "studio_disabled", "Image Studio is not enabled")
	}
	return nil
}

func planImageStudio(ctx context.Context, input ImageStudioCreateInput, hasReference, hasMask, execute bool) (extensionv1.ImageStudioPlan, error) {
	request := extensionv1.ImageStudioPlanRequest{APIKeySelected: input.APIKeyID > 0, Mode: string(input.Mode), Model: input.Model, PromptBytes: len(strings.TrimSpace(input.Prompt)), Count: input.Count, Size: input.Size, Quality: input.Quality, HasReference: hasReference, HasMask: hasMask}
	if err := ctx.Err(); err != nil {
		return extensionv1.ImageStudioPlan{}, newImageStudioError(503, "studio_unavailable", "Image Studio is unavailable")
	}
	plan, code, message := planImageStudioRequest(request)
	if code != "" {
		return plan, newImageStudioError(400, code, message)
	}
	if config, _ := currentImageToolsConfig(); execute && !config.StudioEnabled {
		return plan, newImageStudioError(404, "studio_disabled", "Image Studio is not enabled")
	}
	return plan, nil
}

func PlanImageStudioExecution(ctx context.Context, request ImageStudioExecutionRequest) (extensionv1.ImageStudioPlan, error) {
	keyID := request.Job.APIKeyID
	if request.APIKey != nil {
		keyID = request.APIKey.ID
	}
	return planImageStudio(ctx, ImageStudioCreateInput{APIKeyID: keyID, Mode: request.Job.Mode, Model: request.Job.Model, Prompt: request.Job.Prompt, Count: 1, Size: request.Job.Size, Quality: request.Job.Quality}, len(request.Reference) > 0, len(request.Mask) > 0, true)
}
