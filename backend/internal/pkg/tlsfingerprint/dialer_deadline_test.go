package tlsfingerprint

import (
	"bufio"
	"context"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestFingerprintDefaultDialDeadline(t *testing.T) {
	dialer := newTCPDialer()
	if dialer.Timeout != 10*time.Second || dialer.KeepAlive != 30*time.Second {
		t.Fatal("fingerprint TCP dialer lost bounded setup or keepalive")
	}
	d := NewDialer(nil, func(ctx context.Context, _, _ string) (net.Conn, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > connectTimeout || time.Until(deadline) <= 0 {
			t.Error("custom base dialer did not receive the setup deadline")
		}
		return nil, context.Canceled
	})
	_, err := d.DialTLSContext(context.Background(), "tcp", "example.invalid:443")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestFingerprintStalledSetupCancellation(t *testing.T) {
	for _, mode := range []string{"direct", "http", "socks5"} {
		for _, cancelNow := range []bool{false, true} {
			t.Run(mode+"/"+strconv.FormatBool(cancelNow), func(t *testing.T) {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = listener.Close() }()
				accepted := make(chan struct{})
				done := make(chan struct{})
				go func() {
					defer close(done)
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					defer func() { _ = conn.Close() }()
					close(accepted)
					_, _ = io.Copy(io.Discard, conn)
				}()
				ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
				defer cancel()
				if cancelNow {
					go func() { <-accepted; cancel() }()
				}
				proxyURL := &url.URL{Scheme: mode, Host: listener.Addr().String()}
				started := time.Now()
				var conn net.Conn
				switch mode {
				case "direct":
					conn, err = NewDialer(nil, nil).DialTLSContext(ctx, "tcp", listener.Addr().String())
				case "http":
					conn, err = NewHTTPProxyDialer(nil, proxyURL).DialTLSContext(ctx, "tcp", "example.invalid:443")
				case "socks5":
					conn, err = NewSOCKS5ProxyDialer(nil, proxyURL).DialTLSContext(ctx, "tcp", "example.invalid:443")
				}
				if conn != nil {
					_ = conn.Close()
					t.Fatal("stalled peer yielded a connection")
				}
				if err == nil || time.Since(started) > 2*time.Second {
					t.Fatalf("setup was not interrupted: %v", err)
				}
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("failed setup leaked the socket")
				}
			})
		}
	}
}

func TestFingerprintSetupDeadlineCleared(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close(); _ = server.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	finish, err := setupDeadline(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if err = finish(); err != nil {
		t.Fatal(err)
	}
	cancel()
	go func() { _, _ = server.Write([]byte{42}) }()
	var value [1]byte
	if _, err := client.Read(value[:]); err != nil || value[0] != 42 {
		t.Fatalf("setup cancellation poisoned reusable socket: %v", err)
	}
}

// Trust only the generated test certificate in an isolated test process.
// Production certificate verification and fingerprint profiles are unchanged.
func TestFingerprintHandshakeAndProxyReuse(t *testing.T) {
	if os.Getenv("SUB2_FINGERPRINT_TEST_ROOTS") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestFingerprintHandshakeAndProxyReuse$")
		cmd.Env = append(os.Environ(), "SUB2_FINGERPRINT_TEST_ROOTS=1", "GODEBUG=x509usefallbackroots=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("TLS verification helper: %v\n%s", err, out)
		}
		return
	}
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy authentication reached the origin")
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer origin.Close()
	pool := x509.NewCertPool()
	pool.AddCert(origin.Certificate())
	x509.SetFallbackRoots(pool)
	target := strings.TrimPrefix(origin.URL, "https://")
	for _, mode := range []string{"direct", "http", "socks5"} {
		t.Run(mode, func(t *testing.T) {
			var dial func(context.Context, string, string) (net.Conn, error)
			if mode == "direct" {
				dial = NewDialer(nil, nil).DialTLSContext
			} else {
				address, closeProxy := fingerprintTestProxy(t, mode, target)
				defer closeProxy()
				p := &url.URL{Scheme: mode, Host: address}
				if mode == "http" {
					dial = NewHTTPProxyDialer(nil, p).DialTLSContext
				} else {
					dial = NewSOCKS5ProxyDialer(nil, p).DialTLSContext
				}
			}
			transport := &http.Transport{DialTLSContext: dial}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			for i := 0; i < 2; i++ {
				resp, err := client.Get(origin.URL)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if err != nil || string(body) != "ok" {
					t.Fatalf("exchange failed: %v", err)
				}
			}
		})
	}
}

func fingerprintTestProxy(t *testing.T, mode, target string) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
				br := bufio.NewReader(conn)
				if mode == "http" {
					req, err := http.ReadRequest(br)
					if err != nil || req.Method != "CONNECT" || req.Host != target {
						return
					}
				} else {
					var header [2]byte
					if _, err := io.ReadFull(br, header[:]); err != nil {
						return
					}
					if header[0] != 5 {
						return
					}
					if _, err := io.CopyN(io.Discard, br, int64(header[1])); err != nil {
						return
					}
					if _, err := conn.Write([]byte{5, 0}); err != nil {
						return
					}
					var request [4]byte
					if _, err := io.ReadFull(br, request[:]); err != nil {
						return
					}
					var host string
					switch request[3] {
					case 1:
						ip := make([]byte, 4)
						if _, err := io.ReadFull(br, ip); err != nil {
							return
						}
						host = net.IP(ip).String()
					case 3:
						n, err := br.ReadByte()
						if err != nil {
							return
						}
						data := make([]byte, n)
						if _, err := io.ReadFull(br, data); err != nil {
							return
						}
						host = string(data)
					default:
						return
					}
					var port [2]byte
					if _, err := io.ReadFull(br, port[:]); err != nil {
						return
					}
					if net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port[:])))) != target {
						return
					}
				}
				remote, err := net.DialTimeout("tcp", target, time.Second)
				if err != nil {
					return
				}
				defer func() { _ = remote.Close() }()
				if mode == "http" {
					_, err = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
				} else {
					_, err = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
				}
				if err != nil {
					return
				}
				go func() { _, _ = io.Copy(remote, br); _ = remote.Close() }()
				_, _ = io.Copy(conn, remote)
			}()
		}
	}()
	return listener.Addr().String(), func() { _ = listener.Close() }
}
