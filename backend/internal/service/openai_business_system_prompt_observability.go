package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const businessSystemPromptRequestObservationKey = "openai_business_system_prompt_observation"

type businessSystemPromptObservation struct {
	Applied                   bool
	Revision                  int64
	EffectiveSHA256           string
	BundlePromptEffectiveHash string
	Carrier                   string
	ClientBytes               int
	ServerBytes               int
	MergedBytes               int
	MergedSHA256              string
	IngressTransport          string
	UpstreamTransport         string
	SelectionReason           string
}

func newBusinessSystemPromptObservation(
	application BusinessSystemPromptApplication,
	ingressTransport OpenAIClientTransport,
	upstreamTransport OpenAIUpstreamTransport,
	selectionReason string,
) businessSystemPromptObservation {
	clientInstructions := application.ClientInstructions
	serverInstructions := application.ServerInstructions
	mergedInstructions := ""
	mergedSHA256 := ""
	if application.Applied {
		mergedInstructions = MergeBusinessSystemPromptInstructions(clientInstructions, serverInstructions)
		digest := sha256.Sum256([]byte(mergedInstructions))
		mergedSHA256 = hex.EncodeToString(digest[:])
	}
	effectiveSHA256 := strings.ToLower(strings.TrimSpace(application.EffectiveSHA256))
	if effectiveSHA256 == "" {
		effectiveSHA256 = strings.ToLower(strings.TrimSpace(application.SHA256))
	}
	return businessSystemPromptObservation{
		Applied:                   application.Applied,
		Revision:                  application.Revision,
		EffectiveSHA256:           effectiveSHA256,
		BundlePromptEffectiveHash: strings.ToLower(strings.TrimSpace(application.BundlePromptEffectiveSHA256)),
		Carrier:                   application.Carrier,
		ClientBytes:               len([]byte(clientInstructions)),
		ServerBytes:               len([]byte(serverInstructions)),
		MergedBytes:               len([]byte(mergedInstructions)),
		MergedSHA256:              mergedSHA256,
		IngressTransport:          string(ingressTransport),
		UpstreamTransport:         string(upstreamTransport),
		SelectionReason:           strings.TrimSpace(selectionReason),
	}
}

func logBusinessSystemPromptObservation(
	ctx context.Context,
	c *gin.Context,
	application BusinessSystemPromptApplication,
	upstreamTransport OpenAIUpstreamTransport,
	selectionReason string,
) {
	if c != nil {
		key := businessSystemPromptContextKey(c, businessSystemPromptRequestObservationKey, BusinessSystemPromptProtocolResponses)
		if _, exists := c.Get(key); exists {
			return
		}
		c.Set(key, true)
	}
	observation := newBusinessSystemPromptObservation(
		application,
		GetOpenAIClientTransport(c),
		upstreamTransport,
		selectionReason,
	)
	logger.FromContext(ctx).Info(
		"openai.business_system_prompt_application",
		zap.Bool("applied", observation.Applied),
		zap.Int64("revision", observation.Revision),
		zap.String("effective_sha256", observation.EffectiveSHA256),
		zap.String("bundle_prompt_effective_sha256", observation.BundlePromptEffectiveHash),
		zap.String("carrier", observation.Carrier),
		zap.Int("client_bytes", observation.ClientBytes),
		zap.Int("server_bytes", observation.ServerBytes),
		zap.Int("merged_bytes", observation.MergedBytes),
		zap.String("merged_sha256", observation.MergedSHA256),
		zap.String("ingress_transport", observation.IngressTransport),
		zap.String("upstream_transport", observation.UpstreamTransport),
		zap.String("selection_reason", observation.SelectionReason),
	)
}
