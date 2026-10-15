package codextransport

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testBinding(t *testing.T) Binding {
	t.Helper()
	binding, err := Select("codex-tui/0.162.1 (Windows 10.0.26100; x86_64) WindowsTerminal", "test-account", "http")
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func TestStrictCodexTLSValidationAndHTTP2Streaming(t *testing.T) {
	var calls atomic.Int32
	data := bytes.Repeat([]byte("streaming-window"), 200000)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.ProtoMajor != 2 {
			t.Errorf("expected H2, got %s", r.Proto)
		}
		if r.Header.Get("Accept-Encoding") != "" {
			t.Errorf("unsolicited encoding %s", r.Header.Get("Accept-Encoding"))
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(data)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	for _, trusted := range []bool{false, true} {
		pool := x509.NewCertPool()
		if trusted {
			pool = roots
		}
		rt, err := New(testBinding(t), Options{RootCAs: pool, MaxIdleConns: 2, MaxIdleConnsPerHost: 2, IdleConnTimeout: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		client := &http.Client{Transport: rt, Timeout: 5 * time.Second}
		request, _ := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(`{"input":[]}`))
		resp, err := client.Do(request)
		if !trusted {
			if err == nil {
				_ = resp.Body.Close()
				t.Fatal("untrusted certificate accepted")
			}
			if calls.Load() != 0 {
				t.Fatal("posted a body after certificate failure")
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			got, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr != nil || !bytes.Equal(got, data) {
				t.Fatalf("flow control/streaming: length=%d error=%v", len(got), readErr)
			}
			if resp.TLS == nil || resp.TLS.Version != tls.VersionTLS13 {
				t.Fatal("missing real TLS observation")
			}
		}
		rt.CloseIdleConnections()
	}
	if calls.Load() != 1 {
		t.Fatalf("physical POSTs: %d", calls.Load())
	}
}

func TestStrictCodexUnsupportedAndCanceledRequests(t *testing.T) {
	if _, err := Select("codex-tui/999.0.0 (Windows 10.0.26100; x86_64) WindowsTerminal", "test", "http"); err == nil {
		t.Fatal("accepted unverified version")
	}
	if _, err := New(Binding{Version: LatestVersion()}, Options{}); err == nil {
		t.Fatal("accepted unverified binding")
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	rt, err := New(testBinding(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.CloseIdleConnections()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader("one logical post"))
	if response, err := (&http.Client{Transport: rt}).Do(request); err == nil {
		_ = response.Body.Close()
		t.Fatal("canceled request sent")
	}
	if calls.Load() != 0 {
		t.Fatal("sent a canceled POST")
	}
}

func TestStrictCodexInterruptedResponseRetainsFailureWithoutReplay(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.Header().Set("Content-Length", "64")
		_, _ = w.Write([]byte("incomplete response"))
	}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	rt, err := New(testBinding(t), Options{RootCAs: roots})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.CloseIdleConnections()
	request, _ := http.NewRequest(http.MethodPost, server.URL, strings.NewReader("already sent"))
	response, err := (&http.Client{Transport: rt, Timeout: time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	_, err = io.ReadAll(response.Body)
	if !IsFailure(err) {
		t.Fatalf("interrupted body lost strict failure classification: %v", err)
	}
	if posts.Load() != 1 {
		t.Fatalf("interrupted POST was replayed %d times", posts.Load())
	}
}
