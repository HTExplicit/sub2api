package service

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
)

// IsManagedModelSelector recognizes the reserved server-only model namespace.
// Do not reinterpret arbitrary provider namespaces or private model names.
func IsManagedModelSelector(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "s2pub-")
}

// ContainsManagedModelSelector inspects every top-level model and session.model
// occurrence, including duplicate/case-variant JSON or multipart fields. Model
// properties in input/tool application data are not routing instructions.
func ContainsManagedModelSelector(contentType string, body []byte) bool {
	for _, route := range []string{"", "/v1/live"} {
		for _, model := range requestmodel.FromBodyCandidates(route, contentType, body) {
			if IsManagedModelSelector(model) {
				return true
			}
		}
	}
	return false
}

func managedSelectorWithoutPublishedContext(account *Account, routingModel string) bool {
	if IsManagedModelSelector(routingModel) {
		return true
	}
	// Also prevent an unmanaged channel/private alias from forwarding a
	// reserved selector as if it were a real upstream model name.
	return account != nil && IsManagedModelSelector(account.GetMappedModel(routingModel))
}
