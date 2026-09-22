package extensionv1

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed publisher.json
var publisherJSON []byte

type Publisher struct {
	KeyID     string `json:"key_id"`
	PublicKey string `json:"public_key"`
}

// Publisher metadata has a single source shared by the host and release jobs.
// The matching private key exists only in the repository's signing secret.
func FirstPartyPublisher() Publisher {
	var publisher Publisher
	if err := json.Unmarshal(publisherJSON, &publisher); err != nil {
		panic("invalid embedded publisher metadata")
	}
	return publisher
}

func FirstPartyPublisherKey(keyID, pluginID string) (string, bool) {
	publisher := FirstPartyPublisher()
	if keyID != publisher.KeyID {
		return "", false
	}
	if !strings.HasPrefix(pluginID, "codexrip.") {
		return "", true
	}
	return publisher.PublicKey, true
}
