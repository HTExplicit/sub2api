// codex-transport-probe verifies the transport on a loopback fixture only.
package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codextransport"
	websocket "github.com/Wei-Shaw/sub2api/internal/pkg/codexws"
)

func run() error {
	target := flag.String("url", "", "loopback capture URL")
	ca := flag.String("ca", "", "fixture CA")
	ua := flag.String("ua", "codex-tui/0.162.1 (Windows 10.0.26100; x86_64) WindowsTerminal (codex-tui; 0.162.1)", "fixed identity")
	mode := flag.String("mode", "http", "http or ws")
	flag.Parse()
	u, err := url.Parse(*target)
	if err != nil {
		return err
	}
	if u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1" {
		return fmt.Errorf("probe requires a loopback endpoint")
	}
	certificate, err := os.ReadFile(*ca)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificate) {
		return fmt.Errorf("invalid fixture CA")
	}
	binding, err := codextransport.Select(*ua, "local-fixture", *mode)
	if err != nil {
		return err
	}
	rt, err := codextransport.New(binding, codextransport.Options{RootCAs: roots, MaxIdleConns: 2, MaxIdleConnsPerHost: 2,
		MaxConnsPerHost: 2, IdleConnTimeout: time.Second, ResponseHeaderTimeout: 5 * time.Second})
	if err != nil {
		return err
	}
	defer rt.CloseIdleConnections()
	client := &http.Client{Transport: rt}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	headers := http.Header{}
	for k, v := range map[string]string{"User-Agent": *ua, "Originator": "codex-tui", "Authorization": "Bearer local-dummy-key",
		"X-Codex-Beta-Features": "remote_compaction_v2", "X-Codex-Window-Id": "root:0", "X-Codex-Turn-Metadata": "{}",
		"X-Client-Request-Id": "root", "Session-Id": "root", "Thread-Id": "root", "Content-Type": "application/json", "Accept": "text/event-stream"} {
		headers.Set(k, v)
	}
	if *mode == "ws" {
		u.Scheme = "wss"
		headers.Del("Content-Type")
		headers.Del("Accept")
		headers.Set("OpenAI-Beta", "responses_websockets=2026-02-06")
		conn, _, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPClient: client, HTTPHeader: headers, ClientMaxWindowBits: true, CompressionMode: websocket.CompressionContextTakeover})
		if err != nil {
			return err
		}
		defer conn.CloseNow()
		if err = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","model":"local-fixture","input":[]}`)); err != nil {
			return err
		}
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return err
			}
			if strings.Contains(string(data), `"response.completed"`) {
				return nil
			}
		}
	}
	for range 2 {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewBufferString(`{"model":"local-fixture","input":[]}`))
		if err != nil {
			return err
		}
		request.Header = headers.Clone()
		resp, err := client.Do(request)
		if err != nil {
			return err
		}
		_, readErr := io.Copy(io.Discard, resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("fixture returned %d", resp.StatusCode)
		}
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
