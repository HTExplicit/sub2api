package codextransport

import "testing"

func TestCodexTransportClientVersionIndependentOfReference(t *testing.T) {
	var previous Binding
	for _, version := range []string{"0.162.1", "0.163.0", "0.164.0"} {
		binding, err := Select("codex-tui/"+version+" (Windows 10.0.26100; x86_64) WindowsTerminal", "same-account", "http")
		if err != nil {
			t.Fatal(err)
		}
		if binding.ClientVersion != version || binding.Version != LatestVersion() || !binding.Validated {
			t.Fatalf("client and reference versions were conflated: %+v", binding)
		}
		transport, err := New(binding, Options{})
		if err != nil {
			t.Fatal(err)
		}
		transport.CloseIdleConnections()
		if previous.ClientVersion != "" && previous.Key() == binding.Key() {
			t.Fatal("client versions shared a pool")
		}
		previous = binding
	}
}
