package service

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

var imageToolsConfigOverride atomic.Pointer[extensionv1.ImageToolsConfig]

// currentImageToolsConfig returns the effective Image Studio switch: the
// stored administrator setting once loaded, otherwise the deploy-time flag
// (the value the former image-tools plugin was seeded with).
func currentImageToolsConfig() (extensionv1.ImageToolsConfig, bool) {
	if config := imageToolsConfigOverride.Load(); config != nil {
		return *config, true
	}
	return LegacyImageToolsConfig(), true
}

// imageStudioEnabledFromEnvironment is the deploy-time
// GATEWAY_IMAGE_STUDIO_ENABLED value, read once at startup.
var imageStudioEnabledFromEnvironment = func() bool {
	enabled, _ := config.ResolveImageStudioEnabledFromEnvironment()
	return enabled
}()

// LegacyImageToolsConfig returns the deploy-time switch used until an
// administrator stores image_tools_config.
func LegacyImageToolsConfig() extensionv1.ImageToolsConfig {
	return extensionv1.ImageToolsConfig{StudioEnabled: imageStudioEnabledFromEnvironment}
}

// ImageStudioFeatureEnabled reports whether Image Studio is switched on.
func ImageStudioFeatureEnabled() bool {
	value, _ := currentImageToolsConfig()
	return value.StudioEnabled
}

// imageStudioStop is closed whenever Image Studio is switched off so running
// executions stop, as the former plugin's policy lease did.
var imageStudioStop = struct {
	mu sync.Mutex
	ch chan struct{}
}{ch: make(chan struct{})}

// imageStudioStarter starts the Image Studio background runtime. The server
// registers it so that switching Image Studio on after boot needs no restart.
var imageStudioStarter atomic.Pointer[func()]

// SetImageStudioStarter registers the runtime start used when Image Studio is
// switched on after boot. Starting an already running runtime is a no-op.
func SetImageStudioStarter(start func()) {
	imageStudioStarter.Store(&start)
}

// ConfigureImageTools installs the effective switches (startup load, admin
// update, tests). A nil value falls back to the deploy-time rollout flags.
func ConfigureImageTools(config *extensionv1.ImageToolsConfig) {
	imageToolsConfigOverride.Store(config)
	current, _ := currentImageToolsConfig()
	if current.StudioEnabled {
		if start := imageStudioStarter.Load(); start != nil {
			(*start)()
		}
		return
	}
	imageStudioStop.mu.Lock()
	close(imageStudioStop.ch)
	imageStudioStop.ch = make(chan struct{})
	imageStudioStop.mu.Unlock()
}

// bindImageStudioEnabled returns a context that is canceled when Image Studio
// is switched off. The switch is also registered as a policy signal: upstream
// image IO detaches from request cancellation (detachUpstreamContext) but still
// honours policy signals, so switching off stops an in-flight generation
// instead of billing a result that can no longer be saved.
func bindImageStudioEnabled(ctx context.Context) (context.Context, context.CancelFunc) {
	imageStudioStop.mu.Lock()
	stop := imageStudioStop.ch
	imageStudioStop.mu.Unlock()
	signal, stopSignal := context.WithCancel(context.Background())
	signals, _ := ctx.Value(policyCancellationSignalsKey{}).([]context.Context)
	ctx = context.WithValue(ctx, policyCancellationSignalsKey{}, append(append([]context.Context{}, signals...), signal))
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-stop:
			stopSignal()
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, func() {
		stopSignal()
		cancel()
	}
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
