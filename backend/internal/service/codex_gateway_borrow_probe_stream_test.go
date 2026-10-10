//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type borrowCoreStreamBody struct {
	*io.PipeReader
	closed chan struct{}
	once   sync.Once
}

func (b *borrowCoreStreamBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return b.PipeReader.Close()
}

func TestCodexGatewayBorrowObservationStopsAtTerminal(t *testing.T) {
	for _, action := range []string{"source", "target", "manual", "failed", "done", "no event delimiter", "disconnected", "bounded"} {
		t.Run(action, func(t *testing.T) {
			var bodies []*borrowCoreStreamBody
			var sent []string
			calls := 0
			source, target := borrowCoreAccount(1), borrowCoreAccount(2)
			s := newBorrowCoreTest(t, func(req *http.Request, proxy string, accountID int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
				calls++
				require.Equal(t, "Bearer synthetic-token", req.Header.Get("Authorization"))
				require.Equal(t, "synthetic-chat-id", req.Header.Get("ChatGPT-Account-ID"))
				require.Empty(t, proxy)
				borrowCoreBody(t, req)
				model, answer := "gpt-6-astra", "OK"
				if accountID == 2 {
					model = "gpt-6.1-sol"
				}
				if action == "manual" {
					answer = "<html><svg>鹈鹕骑自行车</svg></html>"
				}
				stream := ": exact raw comment\r\n" + strings.ReplaceAll(borrowCoreSSE(model, answer), "\n", "\r\n")
				if action == "failed" {
					stream = "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"full terminal error\"}}}\r\n\r\n"
				}
				if action == "done" {
					stream = strings.ReplaceAll(stream, "response.completed", "response.done")
				}
				if action == "no event delimiter" {
					stream = strings.TrimSuffix(stream, "\r\n")
				}
				if action == "disconnected" {
					stream = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial answer\"}\r\n\r\n"
				}
				if action == "bounded" {
					stream = strings.Repeat("x", 65)
				}
				reader, writer := io.Pipe()
				body := &borrowCoreStreamBody{PipeReader: reader, closed: make(chan struct{})}
				bodies, sent = append(bodies, body), append(sent, stream)
				go func() {
					_, _ = io.WriteString(writer, stream)
					if action == "target" {
						_ = writer.Close()
						return
					}
					if action == "disconnected" {
						_ = writer.CloseWithError(io.ErrUnexpectedEOF)
						return
					}
					select {
					case <-req.Context().Done():
						_ = writer.CloseWithError(req.Context().Err())
					case <-body.closed:
						_ = writer.Close()
					}
				}()
				headers := make(http.Header)
				if accountID == 1 {
					headers.Set("Set-Cookie", "__oailb=synthetic-borrowed-cookie; Secure; Path=/; Max-Age=230")
				}
				if action == "target" && calls == 1 {
					headers.Set("X-Codex-Turn-State", "minted-state")
				}
				return &http.Response{StatusCode: 200, Header: headers, Body: body}, nil
			}, source, target)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			started := time.Now()
			switch action {
			case "source":
				candidate, _, err := s.acquireSource(ctx, 1)
				require.NoError(t, err)
				require.NotNil(t, candidate)
				require.Equal(t, 1, calls)
			case "target":
				_, template, proxy, err := s.accountTemplate(ctx, 2, "gpt-6.1-sol")
				require.NoError(t, err)
				candidate := &codexGatewayBorrowCandidate{cookie: http.Cookie{Name: "__oailb", Value: "synthetic-borrowed-cookie", Secure: true, Path: "/"}, sourceID: 1, expires: time.Now().Add(codexGatewayBorrowTTL)}
				result := s.probeTarget(ctx, template, target, "gpt-6.1-sol", proxy, nil, candidate)
				require.True(t, result.Success, result.Error)
				require.Equal(t, 2, calls)
			case "manual":
				borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
				_, template, _, err := s.accountTemplate(ctx, 2, "gpt-6.1-sol")
				require.NoError(t, err)
				key := borrowTargetFingerprint(template, target, "gpt-6.1-sol", "", nil, s.candidate.cookie.Value)
				s.targets[codexGatewayBorrowTargetKey{2, "gpt-6.1-sol"}] = codexGatewayBorrowTargetCheck{policyRevision: currentCodexFingerprintPolicyForAccount(target).revision, key: key, cookieKey: borrowHash(s.candidate.cookie.Value), expires: s.candidate.expires, result: CodexGatewayBorrowVerification{Success: true}}
				result, err := s.GeneratePelican(ctx, 2, "gpt-6.1-sol", "high")
				require.NoError(t, err)
				require.Equal(t, "complete", result.Status)
				require.Equal(t, "<html><svg>鹈鹕骑自行车</svg></html>", result.RawAnswer)
				require.Equal(t, sent[0], result.RawResponse)
				require.Equal(t, 1, calls)
			default:
				_, template, _, err := s.accountTemplate(ctx, 1, "gpt-6-astra")
				require.NoError(t, err)
				limit := int64(codexGatewayBorrowProbeMaxBody)
				if action == "bounded" {
					limit = 64
				}
				shot, err := s.fireObservation(ctx, template.Header, source, "gpt-6-astra", "Reply with OK only.", "medium", "", nil, "", nil, HTTPUpstreamProfileCodexBorrowSource, limit)
				require.Equal(t, 1, calls)
				if action == "disconnected" {
					require.ErrorIs(t, err, io.ErrUnexpectedEOF)
					require.False(t, shot.complete)
					require.True(t, shot.incomplete)
					require.Equal(t, "partial answer", shot.answer)
				} else if action == "bounded" {
					require.ErrorContains(t, err, "exceeded 64 bytes")
					require.False(t, shot.complete)
					require.True(t, shot.incomplete)
				} else {
					require.NoError(t, err)
					if action == "failed" {
						require.False(t, shot.complete)
						require.Contains(t, shot.errorText, "full terminal error")
					} else {
						require.True(t, shot.complete)
						require.Equal(t, "OK", shot.answer)
					}
				}
				require.Equal(t, sent[0], shot.raw)
			}
			require.Less(t, time.Since(started), 500*time.Millisecond, "authoritative terminal must not wait for HTTP EOF")
			for _, body := range bodies {
				select {
				case <-body.closed:
				default:
					t.Fatal("response body was not closed before returning")
				}
			}
			require.False(t, errors.Is(ctx.Err(), context.DeadlineExceeded), "the open stream must not force a timeout")
		})
	}
}
