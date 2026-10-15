package codextransport

import (
	"bufio"
	"context"
	"crypto/x509"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func relayFixture(a, b net.Conn) {
	done := make(chan struct{})
	go func() { _, _ = io.Copy(a, b); _ = a.Close(); _ = b.Close(); close(done) }()
	_, _ = io.Copy(b, a)
	_ = a.Close()
	_ = b.Close()
	<-done
}

func TestStrictCodexProxyVariantsPreserveAuthenticationAndStreaming(t *testing.T) {
	var posts atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credential reached target")
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = w.Write(data)
	}))
	defer target.Close()
	targetURL, _ := url.Parse(target.URL)
	for _, kind := range []string{"http", "https", "socks5"} {
		t.Run(kind, func(t *testing.T) {
			roots := x509.NewCertPool()
			roots.AddCert(target.Certificate())
			var proxyURL *url.URL
			if kind == "socks5" {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = listener.Close() }()
				go func() {
					client, err := listener.Accept()
					if err != nil {
						return
					}
					defer func() { _ = client.Close() }()
					_ = client.SetDeadline(time.Now().Add(5 * time.Second))
					head := make([]byte, 2)
					if _, err = io.ReadFull(client, head); err != nil {
						return
					}
					methods := make([]byte, int(head[1]))
					if _, err = io.ReadFull(client, methods); err != nil {
						return
					}
					_, _ = client.Write([]byte{5, 2})
					if _, err = io.ReadFull(client, head); err != nil {
						return
					}
					user := make([]byte, int(head[1]))
					_, _ = io.ReadFull(client, user)
					one := make([]byte, 1)
					_, _ = io.ReadFull(client, one)
					password := make([]byte, int(one[0]))
					_, _ = io.ReadFull(client, password)
					if string(user) != "fixture" || string(password) != "pass" {
						t.Error("SOCKS authentication mismatch")
						return
					}
					_, _ = client.Write([]byte{1, 0})
					request := make([]byte, 4)
					if _, err = io.ReadFull(client, request); err != nil {
						return
					}
					size := 4
					switch request[3] {
					case 3:
						_, _ = io.ReadFull(client, one)
						size = int(one[0])
					case 4:
						size = 16
					}
					address := make([]byte, size+2)
					if _, err = io.ReadFull(client, address); err != nil {
						return
					}
					_, expectedPort, _ := net.SplitHostPort(targetURL.Host)
					port, _ := strconv.Atoi(expectedPort)
					if int(binary.BigEndian.Uint16(address[size:])) != port {
						t.Error("SOCKS target mismatch")
						return
					}
					upstream, err := net.DialTimeout("tcp", targetURL.Host, time.Second)
					if err != nil {
						return
					}
					_, _ = client.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
					_ = client.SetDeadline(time.Time{})
					relayFixture(client, upstream)
				}()
				proxyURL, _ = url.Parse("socks5://fixture:pass@" + listener.Addr().String())
			} else {
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodConnect || r.Host != targetURL.Host {
						http.Error(w, "invalid fixture tunnel", 400)
						return
					}
					if r.Header.Get("Proxy-Authorization") == "" {
						t.Error("missing proxy authentication")
					}
					upstream, err := net.DialTimeout("tcp", targetURL.Host, time.Second)
					if err != nil {
						http.Error(w, "tunnel failed", http.StatusBadGateway)
						return
					}
					hijacker, ok := w.(http.Hijacker)
					if !ok {
						_ = upstream.Close()
						t.Error("fixture does not support hijacking")
						return
					}
					client, buffer, err := hijacker.Hijack()
					if err != nil {
						_ = upstream.Close()
						return
					}
					_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
					_ = buffer.Flush()
					relayFixture(client, upstream)
				})
				var proxy *httptest.Server
				if kind == "https" {
					proxy = httptest.NewTLSServer(handler)
					roots.AddCert(proxy.Certificate())
				} else {
					proxy = httptest.NewServer(handler)
				}
				defer proxy.Close()
				proxyURL, _ = url.Parse(proxy.URL)
				proxyURL.User = url.UserPassword("fixture", "pass")
			}
			rt, err := New(testBinding(t), Options{ProxyURL: proxyURL, RootCAs: roots, MaxIdleConns: 2, MaxIdleConnsPerHost: 2, IdleConnTimeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer rt.CloseIdleConnections()
			body := strings.Repeat("opaque proxy body ", 2048)
			request, _ := http.NewRequest(http.MethodPost, target.URL, strings.NewReader(body))
			response, err := (&http.Client{Transport: rt, Timeout: 5 * time.Second}).Do(request)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil || string(data) != body {
				t.Fatalf("proxy body changed: %v", err)
			}
		})
	}
	if posts.Load() != 3 {
		t.Fatalf("unexpected physical POST count: %d", posts.Load())
	}
}

func TestStrictCodexDoesNotReplaySentPOSTOrCanceledCONNECT(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("fixture does not support hijacking")
			return
		}
		conn, _, err := hijacker.Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	rt, err := New(testBinding(t), Options{RootCAs: roots})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.CloseIdleConnections()
	request, _ := http.NewRequest(http.MethodPost, server.URL, strings.NewReader("body already delivered"))
	request.Header.Set("Idempotency-Key", "fixture-id")
	if response, err := (&http.Client{Transport: rt, Timeout: time.Second}).Do(request); err == nil {
		_ = response.Body.Close()
		t.Fatal("expected an uncertain response failure")
	} else if !IsFailure(err) {
		t.Fatalf("failure lost its no-replay classification: %v", err)
	}
	if posts.Load() != 1 {
		t.Fatalf("sent POST was replayed %d times", posts.Load())
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = http.ReadRequest(bufio.NewReader(conn))
		_, _ = io.Copy(io.Discard, conn)
	}()
	proxyURL, _ := url.Parse("http://" + listener.Addr().String())
	blocked, err := New(testBinding(t), Options{ProxyURL: proxyURL, RootCAs: roots})
	if err != nil {
		t.Fatal(err)
	}
	defer blocked.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	request, _ = http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader("never sent"))
	start := time.Now()
	if response, err := (&http.Client{Transport: blocked}).Do(request); err == nil {
		_ = response.Body.Close()
		t.Fatal("canceled CONNECT succeeded")
	}
	if time.Since(start) > time.Second {
		t.Fatal("CONNECT ignored cancellation")
	}
	if posts.Load() != 1 {
		t.Fatal("canceled tunnel sent an upstream request")
	}
}
