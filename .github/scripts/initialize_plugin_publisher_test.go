package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func publisherMetadataPage(total int, names ...string) []byte {
	secrets := make([]map[string]string, 0, len(names))
	for _, name := range names {
		secrets = append(secrets, map[string]string{"name": name})
	}
	raw, _ := json.Marshal(map[string]any{"total_count": total, "secrets": secrets})
	return raw
}

func publisherFirstPage() []string {
	names := make([]string, 100)
	for index := range names {
		names[index] = fmt.Sprintf("UNRELATED_%03d", index)
	}
	return names
}

func TestPublisherSecretInventoryFindsLaterPageWithoutInitializing(t *testing.T) {
	calls := 0
	exists, err := publisherSecretExists(context.Background(), func(_ context.Context, input []byte, args ...string) ([]byte, error) {
		calls++
		if input != nil || len(args) < 2 || args[0] != "api" {
			t.Fatal("only read-only metadata is allowed in this fixture")
		}
		if strings.Contains(strings.Join(args, " "), "page=2") {
			return publisherMetadataPage(101, publisherSecret), nil
		}
		return publisherMetadataPage(101, publisherFirstPage()...), nil
	})
	if err != nil || !exists || calls != 2 {
		t.Fatalf("later-page key must prevent initialization: exists=%v calls=%d err=%v", exists, calls, err)
	}
}

func TestPublisherSecretInventoryIncompleteOrFailedPageIsNotAbsence(t *testing.T) {
	for _, mode := range []string{"api-error", "incomplete", "changed-total", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			_, err := publisherSecretExists(context.Background(), func(_ context.Context, _ []byte, _ ...string) ([]byte, error) {
				calls++
				if calls == 1 {
					return publisherMetadataPage(101, publisherFirstPage()...), nil
				}
				switch mode {
				case "api-error":
					return nil, errors.New("synthetic metadata failure")
				case "incomplete":
					return publisherMetadataPage(101), nil
				case "changed-total":
					return publisherMetadataPage(100, publisherSecret), nil
				default:
					return publisherMetadataPage(101, "UNRELATED_000"), nil
				}
			})
			if err == nil {
				t.Fatal("an incomplete metadata inventory must never authorize a new publisher key")
			}
		})
	}
}

func TestPublisherSecretInventoryRequiresExplicitCompleteMetadata(t *testing.T) {
	for _, raw := range []string{`{}`, `{"total_count":0}`, `{"total_count":1,"secrets":[]}`} {
		_, err := publisherSecretExists(context.Background(), func(context.Context, []byte, ...string) ([]byte, error) { return []byte(raw), nil })
		if err == nil {
			t.Fatalf("invalid inventory accepted: %s", raw)
		}
	}
	exists, err := publisherSecretExists(context.Background(), func(context.Context, []byte, ...string) ([]byte, error) { return publisherMetadataPage(0), nil })
	if err != nil || exists {
		t.Fatalf("complete empty inventory rejected: exists=%v err=%v", exists, err)
	}
}
