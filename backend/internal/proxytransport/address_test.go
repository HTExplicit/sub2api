package proxytransport

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestProxyCandidateFormatsAndCredentialRules(t *testing.T) {
	for _, tc := range []struct {
		name, raw, hint, protocol, host, user, password string
		port                                            int
	}{
		{"uri http", "http://u:p@Proxy.Example:8080", "socks5", "http", "Proxy.Example", "u", "p", 8080},
		{"uri https default", "https://Proxy.Example", "http", "https", "Proxy.Example", "", "", 443},
		{"uri socks", "socks5://u:p@proxy.example:1080", "https", "socks5", "proxy.example", "u", "p", 1080},
		{"uri socks remote dns", "socks5h://u:p@proxy.example:1080", "http", "socks5h", "proxy.example", "u", "p", 1080},
		{"authority", "proxy.example:3128", "socks5h", "socks5h", "proxy.example", "", "", 3128},
		{"auth first", "user:p%2540ss@proxy.example:8080", "", "http", "proxy.example", "user", "p%40ss", 8080},
		{"auth last", "proxy.example:8080@user:p@ss:#", "", "http", "proxy.example", "user", "p@ss:#", 8080},
		{"four host first", "proxy.example:8080:user:p:a%40 ss", "", "http", "proxy.example", "user", "p:a%40 ss", 8080},
		{"four auth first", "user:p%40ss:proxy.example:8080", "", "http", "proxy.example", "user", "p%40ss", 8080},
		{"space columns", "proxy.example 8080 user ' p a '", "", "http", "proxy.example", "user", " p a ", 8080},
		{"tab columns", "user\tp%40ss\tproxy.example\t8080", "", "http", "proxy.example", "user", "p%40ss", 8080},
		{"comma columns", "proxy.example,8080,\" user \",\"p,a|b;c\"", "", "http", "proxy.example", " user ", "p,a|b;c", 8080},
		{"pipe columns", "user|\" p|a \"|proxy.example|8080", "", "http", "proxy.example", "user", " p|a ", 8080},
		{"semicolon columns", "proxy.example;8080;user;\"p;a\"", "", "http", "proxy.example", "user", "p;a", 8080},
		{"ipv6", "[2001:db8::1]:8080", "", "http", "2001:db8::1", "", "", 8080},
		{"ipv6 four", "[2001:db8::1]:8080:user:p", "", "http", "2001:db8::1", "user", "p", 8080},
		{"ipv6 auth first", "user:p:[2001:db8::1]:8080", "", "http", "2001:db8::1", "user", "p", 8080},
		{"english labels", "password: p@ss:#\nPort: 2721\nProxy Server: proxy.example\nusername: u+s", "", "http", "proxy.example", "u+s", "p@ss:#", 2721},
		{"chinese labels", "协议：socks5h 主机：proxy.example 端口：8080 用户名：user 密码：\" p，；： 空格 \"", "https", "socks5h", "proxy.example", "user", " p，；： 空格 ", 8080},
		{"label continuation", "host=proxy.example port=8080\nusername=user\npassword=secret", "", "http", "proxy.example", "user", "secret", 8080},
		{"IP labels CR", "IP: 127.0.0.1\r端口: 8080\r用户名: user\r密码: p", "", "http", "127.0.0.1", "user", "p", 8080},
		{"address labels", "代理地址: [::1]\n端口: 8080\n用户名: user\n密码: p;", "", "http", "::1", "user", "p;", 8080},
		{"URI label protocol", "host: https://user:p@Proxy.Example:8080\nprotocol: https", "socks5", "https", "Proxy.Example", "user", "p", 8080},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parsed := Parse(tc.raw, tc.hint)
			if len(parsed.Candidates) != 1 || len(parsed.Issues) != 0 || parsed.SelectionRequired || parsed.SelectionID == "" {
				t.Fatalf("expected one clean candidate, got %d and issues %+v", len(parsed.Candidates), parsed.Issues)
			}
			normal, err := Resolve(tc.raw, tc.hint, parsed.SelectionID)
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(normal)
			if err != nil {
				t.Fatal("invalid normalized URL")
			}
			user, password := "", ""
			if u.User != nil {
				user = u.User.Username()
				password, _ = u.User.Password()
			}
			if u.Scheme != tc.protocol || u.Hostname() != tc.host || user != tc.user || password != tc.password || effectivePort(u) != tc.port {
				t.Fatal("selected endpoint changed protocol, host, port or credential bytes")
			}
		})
	}
}

func TestProxyCandidatesAmbiguityDedupeSelectionAndRedaction(t *testing.T) {
	raw := "proxy.example:8080:user:1234"
	parsed := Parse(raw, "http")
	if len(parsed.Candidates) != 2 || !parsed.SelectionRequired || parsed.SelectionID != "" {
		t.Fatal("both four-field orders must remain candidates")
	}
	_, err := Resolve(raw, "http", "")
	var selection *SelectionError
	if !errors.As(err, &selection) || selection.Code != "proxy_selection_required" {
		t.Fatal("ambiguous input did not require selection")
	}
	for _, item := range parsed.Candidates {
		if _, err := Resolve(raw, "http", item.SelectionID); err != nil {
			t.Fatal(err)
		}
		for _, changed := range []struct{ raw, hint string }{{raw + " ", "http"}, {raw, "https"}, {"single.example:80", "http"}} {
			_, err := Resolve(changed.raw, changed.hint, item.SelectionID)
			if !errors.As(err, &selection) || selection.Code != "proxy_selection_stale" {
				t.Fatal("changed draft accepted old selection")
			}
		}
	}
	raw = "http://fixture-user:fixture-password@Proxy.Example:80\nhttp://fixture-user:fixture-password@proxy.example\nhttp://fixture-user:different-password@proxy.example:80"
	parsed = Parse(raw, "socks5")
	if len(parsed.Candidates) != 2 {
		t.Fatal("dedupe must include complete credentials and effective endpoint")
	}
	encoded, _ := json.Marshal(parsed)
	for _, secret := range []string{"fixture-user", "fixture-password", "different-password", "http://"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("candidate serialization leaked credential material")
		}
	}
	preserved, err := Resolve(raw, "socks5", parsed.Candidates[0].SelectionID)
	if err != nil || preserved != strings.Split(raw, "\n")[0] {
		t.Fatal("existing explicit URI representation changed")
	}
}

func TestProxyCandidatesKeepValidRecordsAndRejectConflictingFields(t *testing.T) {
	raw := "http://u:first@one.example:80; http://u:second@one.example:80\r\nbad.example:99999\r[::1]:8080"
	parsed := Parse(raw, "http")
	if len(parsed.Candidates) != 3 || len(parsed.Issues) != 1 || parsed.Issues[0].Line != 2 || parsed.Candidates[2].SourceLine != 3 || parsed.Candidates[1].SourceColumn <= 1 {
		t.Fatalf("multi-record provenance or validation incorrect: %d candidates, issues %+v", len(parsed.Candidates), parsed.Issues)
	}
	for _, item := range parsed.Candidates {
		if _, err := Resolve(raw, "http", item.SelectionID); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{
		"host: proxy.example\nport: 8080\nport: 9090",
		"host: proxy.example\nport: 8080\nusername: user",
		"host: https://proxy.example:8080\nprotocol: http",
		"host: proxy.example:8080\nport: 9090",
		"host: http://user:secret@proxy.example:80\nusername: other\npassword: secret",
		"2001:db8::1:1080",
		"ftp://user:secret@proxy.example:21",
		"proxy.example,8080,user,\"unclosed",
	} {
		parsed := Parse(raw, "http")
		if len(parsed.Candidates) != 0 || len(parsed.Issues) == 0 {
			t.Fatal("invalid or contradictory fields produced a selectable proxy")
		}
	}
}

func TestProxyEndpointTransportIsStrictAndPreservesRepresentation(t *testing.T) {
	for _, raw := range []string{"host:8080", "host:8080:user:password", "user:password@host:8080", "http://host:8080\nhttp://other:8080", "http://host:99999", "http://2001:db8::1:8080"} {
		if _, err := ParseEndpoint(raw); err == nil {
			t.Fatal("transport accepted a draft instead of a validated endpoint")
		}
	}
	for _, raw := range []string{"http://U:p%2540@Proxy.Example:80", "https://Proxy.Example", "socks5://u:p@[::1]:1080"} {
		normal, err := Normalize(raw)
		if err != nil || normal != raw {
			t.Fatal("valid saved representation or protocol changed")
		}
	}
}

func TestProxyCredentialsAreNotFormatDiscriminators(t *testing.T) {
	for _, tc := range []struct{ raw, user, password string }{
		{"proxy.example:8080:user:http://literal", "user", "http://literal"},
		{"user:http://literal:proxy.example:8080", "user", "http://literal"},
		{`" user:literal ":" p@ss ,;| "@[::1]:8080`, " user:literal ", " p@ss ,;| "},
		{`user:"%40"@proxy.example:8080`, "user", "%40"},
	} {
		parsed := Parse(tc.raw, "http")
		if len(parsed.Candidates) != 1 || len(parsed.Issues) > 0 {
			t.Fatalf("credential text changed format detection: %d candidates, %+v", len(parsed.Candidates), parsed.Issues)
		}
		normal, err := Resolve(tc.raw, "http", parsed.SelectionID)
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(normal)
		password, _ := u.User.Password()
		if u.User.Username() != tc.user || password != tc.password {
			t.Fatal("quoted credential bytes changed")
		}
	}
	if got := Parse("http://host:80:user:1234", "http"); len(got.Candidates) != 0 {
		t.Fatal("malformed explicit URI fell back to a different field order")
	}
	parsed := Parse("http://[::1]:80\nhttp://[0:0:0:0:0:0:0:1]", "")
	if len(parsed.Candidates) != 1 {
		t.Fatal("equivalent IPv6 endpoints were not deduplicated")
	}
	_, err := Resolve("http://[::1]:80\nhttp://[0:0:0:0:0:0:0:1]", "http", parsed.SelectionID)
	var invalid *SelectionError
	if !errors.As(err, &invalid) || invalid.Code != "proxy_selection_stale" {
		t.Fatal("changed protocol input did not invalidate selection")
	}
}
