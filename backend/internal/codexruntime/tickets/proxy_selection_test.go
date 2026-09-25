package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/Wei-Shaw/sub2api/internal/proxytransport"
)

func TestProxySelectionSaveAndTestUseIdenticalEndpoint(t *testing.T) {
	m := NewModule()
	defer m.cancel()
	m.SetHost(&memoryHost{})
	input := "http://u:one@first.example:80\nsocks5://u:two@[::1]:1080"
	parsed := proxytransport.Parse(input, "https")
	if len(parsed.Candidates) != 2 {
		t.Fatal("fixture must contain two candidates")
	}
	selection := parsed.Candidates[1].SelectionID
	request := map[string]string{"proxy_url": input, "proxy_protocol": "https", "proxy_selection_id": selection}
	raw, _ := json.Marshal(request)
	normal, err := m.ValidateConfig(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	var saved Config
	if json.Unmarshal(normal, &saved) != nil {
		t.Fatal("invalid saved config")
	}
	if saved.ProxyURL != "socks5://u:two@[::1]:1080" || saved.ProxyProtocol != "" || saved.ProxySelectionID != "" || strings.Contains(string(normal), "first.example") {
		t.Fatal("save retained a draft, changed explicit protocol, or persisted a selection")
	}
	var endpoint string
	m.prepareProxy = func(_ context.Context, _ HostCaller, normal string, explicit bool) (*http.Client, *ProxyResult, error) {
		if !explicit {
			t.Error("proxy test must retain explicit certificate policy")
		}
		endpoint = normal
		return nil, &ProxyResult{Code: "fixture", Protocol: "socks5"}, nil
	}
	invoke := func(id string) error {
		payload, _ := json.Marshal(map[string]string{"proxy_url": input, "protocol": "https", "proxy_selection_id": id})
		_, err := m.Invoke(context.Background(), extensionv1.Invocation{Capability: extensionv1.CapabilityAdmin, Operation: "proxy.test", Payload: payload})
		return err
	}
	if err := invoke(selection); err != nil {
		t.Fatal(err)
	}
	if endpoint != saved.ProxyURL {
		t.Fatal("save and test selected different endpoints")
	}
	for _, id := range []string{"", "outdated"} {
		request["proxy_selection_id"] = id
		raw, _ = json.Marshal(request)
		_, saveErr := m.ValidateConfig(context.Background(), raw)
		testErr := invoke(id)
		var saveSelection, testSelection *proxytransport.SelectionError
		if !errors.As(saveErr, &saveSelection) || !errors.As(testErr, &testSelection) || saveSelection.Code != testSelection.Code {
			t.Fatal("save and test must reject with the same selection error")
		}
	}
}
