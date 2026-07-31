package httputil

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/tidwall/gjson"
)

type terminalErrorReader struct {
	err error
}

func (r terminalErrorReader) Read(_ []byte) (int, error) {
	return 0, r.err
}

func requestWithTerminalError(t *testing.T, body []byte, encoding string, terminalErr error) *http.Request {
	t.Helper()
	req := newRequestWithBody(t, body, encoding)
	req.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), terminalErrorReader{err: terminalErr}))
	req.ContentLength = int64(len(body) + 1)
	return req
}

func TestNormalizeLenientJSONRequestBody_accepts_client_control_chars_in_strings(t *testing.T) {
	tests := []struct {
		name    string
		body    []byte
		path    string
		want    string
		wantRaw string
	}{
		{
			name:    "null byte in message content",
			body:    []byte("{\"messages\":[{\"content\":\"hello\x00world\"}]}"),
			path:    "messages.0.content",
			want:    "hello\x00world",
			wantRaw: `"hello\u0000world"`,
		},
		{
			name:    "ansi escape in message content",
			body:    []byte("{\"messages\":[{\"content\":\"hello\x1b[31mred\x1b[0m\"}]}"),
			path:    "messages.0.content",
			want:    "hello\x1b[31mred\x1b[0m",
			wantRaw: `"hello\u001b[31mred\u001b[0m"`,
		},
		{
			name:    "leading UTF-8 BOM",
			body:    []byte("\xef\xbb\xbf{\"input\":\"hello\"}"),
			path:    "input",
			want:    "hello",
			wantRaw: `"hello"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			if gjson.ValidBytes(tt.body) {
				t.Fatalf("test payload should reproduce strict JSON rejection: %q", tt.body)
			}

			// When
			got, err := NormalizeLenientJSONRequestBody(tt.body, 1024)
			if err != nil {
				t.Fatalf("NormalizeLenientJSONRequestBody: %v", err)
			}

			// Then
			if !gjson.ValidBytes(got) {
				t.Fatalf("normalized body should be valid JSON: %q", got)
			}
			result := gjson.GetBytes(got, tt.path)
			if result.String() != tt.want {
				t.Fatalf("value mismatch: got %q want %q", result.String(), tt.want)
			}
			if result.Raw != tt.wantRaw {
				t.Fatalf("raw value mismatch: got %q want %q", result.Raw, tt.wantRaw)
			}
		})
	}
}

func TestNormalizeLenientJSONRequestBody_keeps_invalid_structure_invalid(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{
			name: "truncated JSON",
			body: []byte("{\"messages\":[{\"content\":\"hello\"}]"),
		},
		{
			name: "control character outside string",
			body: []byte("{\"input\":\"hello\"}\x00"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When
			got, err := NormalizeLenientJSONRequestBody(tt.body, 1024)
			if err != nil {
				t.Fatalf("NormalizeLenientJSONRequestBody: %v", err)
			}

			// Then
			if gjson.ValidBytes(got) {
				t.Fatalf("normalization must not repair invalid JSON structure: %q", got)
			}
		})
	}
}

func TestNormalizeLenientJSONRequestBody_allows_http_requests_with_client_control_chars(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Given
		body, err := ReadLenientJSONRequestBodyWithPrealloc(r, 1024)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// When
		if !gjson.ValidBytes(body) {
			http.Error(w, "Failed to parse request body", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	tests := []struct {
		name string
		body []byte
		want int
	}{
		{
			name: "null byte in JSON string",
			body: []byte("{\"model\":\"gpt-5.5\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\x00world\"}]}"),
			want: http.StatusAccepted,
		},
		{
			name: "ANSI escape in JSON string",
			body: []byte("{\"model\":\"gpt-5.5\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\x1b[31mred\x1b[0m\"}]}"),
			want: http.StatusAccepted,
		},
		{
			name: "leading UTF-8 BOM",
			body: []byte("\xef\xbb\xbf{\"model\":\"gpt-5.5\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}]}"),
			want: http.StatusAccepted,
		},
		{
			name: "truncated JSON",
			body: []byte("{\"model\":\"gpt-5.5\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}]"),
			want: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", bytes.NewReader(tt.body))
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")

			resp, err := server.Client().Do(req)
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != tt.want {
				t.Fatalf("status mismatch: got %d want %d", resp.StatusCode, tt.want)
			}
		})
	}
}

func TestNormalizeLenientJSONRequestBody_rejects_expansion_past_limit(t *testing.T) {
	// Given
	body := []byte("{\"input\":\"\x00\x00\"}")

	// When
	_, err := NormalizeLenientJSONRequestBody(body, int64(len(body)+5))

	// Then
	var maxErr *http.MaxBytesError
	if !errors.As(err, &maxErr) {
		t.Fatalf("expected MaxBytesError, got %T %v", err, err)
	}
	if maxErr.Limit != int64(len(body)+5) {
		t.Fatalf("limit mismatch: got %d want %d", maxErr.Limit, len(body)+5)
	}
}

func TestReadRequestBodyWithPrealloc_preserves_identity_bytes_on_unexpected_eof(t *testing.T) {
	req := requestWithTerminalError(t, []byte(samplePayload), "", io.ErrUnexpectedEOF)

	body, err := ReadRequestBodyWithPrealloc(req)

	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected unexpected EOF, got %T %v", err, err)
	}
	if string(body) != samplePayload {
		t.Fatalf("partial body mismatch: got %q", body)
	}
	var readErr *RequestBodyReadError
	if !errors.As(err, &readErr) {
		t.Fatalf("expected RequestBodyReadError, got %T", err)
	}
	if readErr.Stage != "read" || readErr.ReceivedBytes != int64(len(samplePayload)) {
		t.Fatalf("unexpected read metadata: %+v", readErr)
	}
}

func TestReadLenientJSONRequestBodyWithPrealloc_recovers_complete_json_on_unexpected_eof(t *testing.T) {
	req := requestWithTerminalError(t, []byte(samplePayload), "", io.ErrUnexpectedEOF)

	body, err := ReadLenientJSONRequestBodyWithPrealloc(req, 1024)

	if err != nil {
		t.Fatalf("expected complete JSON recovery, got %v", err)
	}
	if string(body) != samplePayload {
		t.Fatalf("recovered body mismatch: got %q", body)
	}
}

func TestReadLenientJSONRequestBodyWithPrealloc_rejects_truncated_json_on_unexpected_eof(t *testing.T) {
	req := requestWithTerminalError(t, []byte(`{"input":"unfinished`), "", io.ErrUnexpectedEOF)

	_, err := ReadLenientJSONRequestBodyWithPrealloc(req, 1024)

	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected unexpected EOF, got %T %v", err, err)
	}
}

func TestReadLenientJSONRequestBodyWithPrealloc_rejects_non_eof_read_error(t *testing.T) {
	terminalErr := errors.New("synthetic read failure")
	req := requestWithTerminalError(t, []byte(samplePayload), "", terminalErr)

	_, err := ReadLenientJSONRequestBodyWithPrealloc(req, 1024)

	if !errors.Is(err, terminalErr) {
		t.Fatalf("expected synthetic read failure, got %T %v", err, err)
	}
}

func TestReadLenientJSONRequestBodyWithPrealloc_recovers_decoded_gzip_on_transport_eof(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte(samplePayload)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	req := requestWithTerminalError(t, compressed.Bytes(), "gzip", io.ErrUnexpectedEOF)

	body, err := ReadLenientJSONRequestBodyWithPrealloc(req, 1024)

	if err != nil {
		t.Fatalf("expected decoded JSON recovery, got %v", err)
	}
	if string(body) != samplePayload {
		t.Fatalf("recovered body mismatch: got %q", body)
	}
	if req.Header.Get("Content-Encoding") != "" || req.ContentLength != int64(len(samplePayload)) {
		t.Fatalf("decoded request metadata not normalized")
	}
}

func TestReadLenientJSONRequestBodyWithPrealloc_recovers_decoded_zstd_on_transport_eof(t *testing.T) {
	writer, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	compressed := writer.EncodeAll([]byte(samplePayload), nil)
	if err := writer.Close(); err != nil {
		t.Fatalf("zstd close: %v", err)
	}
	req := requestWithTerminalError(t, compressed, "zstd", io.ErrUnexpectedEOF)

	body, err := ReadLenientJSONRequestBodyWithPrealloc(req, 1024)

	if err != nil {
		t.Fatalf("expected decoded JSON recovery, got %v", err)
	}
	if string(body) != samplePayload {
		t.Fatalf("recovered body mismatch: got %q", body)
	}
}

func TestReadLenientJSONRequestBodyWithPrealloc_recovers_gzip_payload_with_missing_footer(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte(samplePayload)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	withoutFooter := compressed.Bytes()[:compressed.Len()-8]
	req := newRequestWithBody(t, withoutFooter, "gzip")

	body, err := ReadLenientJSONRequestBodyWithPrealloc(req, 1024)

	if err != nil {
		t.Fatalf("expected complete decoded JSON recovery, got %v", err)
	}
	if string(body) != samplePayload {
		t.Fatalf("recovered body mismatch: got %q", body)
	}
}

func TestReadLenientJSONRequestBodyWithPrealloc_rejects_incomplete_gzip_json(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte(`{"input":"unfinished`)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	withoutFooter := compressed.Bytes()[:compressed.Len()-8]
	req := newRequestWithBody(t, withoutFooter, "gzip")

	_, err := ReadLenientJSONRequestBodyWithPrealloc(req, 1024)

	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected unexpected EOF, got %T %v", err, err)
	}
}
