package codextransport

import (
	"bytes"
	"net"
	"testing"
	"time"
)

type memoryConn struct{ bytes.Buffer }

func (*memoryConn) Close() error                     { return nil }
func (*memoryConn) LocalAddr() net.Addr              { return nil }
func (*memoryConn) RemoteAddr() net.Addr             { return nil }
func (*memoryConn) SetDeadline(time.Time) error      { return nil }
func (*memoryConn) SetReadDeadline(time.Time) error  { return nil }
func (*memoryConn) SetWriteDeadline(time.Time) error { return nil }

func TestHTTP1HeaderSpellingPreservesOpaqueBodiesAndReuse(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		t.Run(map[bool]string{false: "content-length", true: "chunked"}[chunked], func(t *testing.T) {
			body := "Host: should-stay\r\nUser-Agent: body-text"
			framing := "Content-Length: 39\r\n"
			if chunked {
				framing = "Transfer-Encoding: chunked\r\n"
				body = "4\r\nHost\r\n6\r\n: body\r\n0\r\nUser-Agent: trailer\r\n\r\n"
			}
			first := "POST / HTTP/1.1\r\nUser-Agent: test\r\nHost: localhost\r\n" + framing + "\r\n" + body
			second := "GET /two HTTP/1.1\r\nHost: localhost\r\n\r\n"
			wire := &memoryConn{}
			conn := &http1WireConn{Conn: wire, spelling: map[string]string{"host": "host", "user-agent": "user-agent", "content-length": "content-length"}}
			input := first + second
			for start := 0; start < len(input); {
				end := start + 3
				if end > len(input) {
					end = len(input)
				}
				if n, err := conn.Write([]byte(input[start:end])); err != nil || n != end-start {
					t.Fatalf("write %d: %d %v", start, n, err)
				}
				start = end
			}
			expected := "POST / HTTP/1.1\r\nuser-agent: test\r\nhost: localhost\r\n" + framing + "\r\n" + body + "GET /two HTTP/1.1\r\nhost: localhost\r\n\r\n"
			if !chunked {
				expected = string(bytes.ReplaceAll([]byte(expected), []byte("Content-Length:"), []byte("content-length:")))
			}
			if wire.String() != expected {
				t.Fatalf("body/trailer or reused headers changed:\n%q\nwant:\n%q", wire.String(), expected)
			}
		})
	}
}
