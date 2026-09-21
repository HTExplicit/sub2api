package service

import (
	"context"
	"encoding/json"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type pluginExpectedPackageKey struct{}

func WithPluginExpectedPackage(ctx context.Context, digest string) context.Context {
	return context.WithValue(ctx, pluginExpectedPackageKey{}, digest)
}

func PluginExpectedPackage(ctx context.Context) string {
	digest, _ := ctx.Value(pluginExpectedPackageKey{}).(string)
	return digest
}

func validatePluginPrecondition(ctx context.Context, installation *PluginInstallation) error {
	if installation == nil || (PluginExpectedRevision(ctx) > 0 && installation.Revision != PluginExpectedRevision(ctx)) ||
		(PluginExpectedPackage(ctx) != "" && installation.PackageSHA256 != PluginExpectedPackage(ctx)) {
		return ErrPluginStateChanged
	}
	return nil
}

type PluginConfigSnapshot struct {
	Config        json.RawMessage
	Revision      int64
	PackageSHA256 string
}

// Production HTTP saves need a receipt from the conditional write itself,
// not an unrelated later read that may belong to another administrator.
type PluginConfigRevisionRepository interface {
	UpdateConfigReturningRevision(context.Context, int64, string, string) (int64, error)
}

var ErrPluginConfigReceiptUnavailable = infraerrors.ServiceUnavailable("PLUGIN_CONFIG_RECEIPT_UNAVAILABLE", "plugin configuration revision receipt is unavailable")
