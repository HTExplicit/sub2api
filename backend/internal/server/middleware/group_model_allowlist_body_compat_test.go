package middleware

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/gin-gonic/gin"
)

type groupAllowlistTerminalBody struct {
	data        []byte
	terminalErr error
}

func (b *groupAllowlistTerminalBody) Read(p []byte) (int, error) {
	if len(b.data) > 0 {
		n := copy(p, b.data)
		b.data = b.data[n:]
		return n, nil
	}
	if b.terminalErr != nil {
		err := b.terminalErr
		b.terminalErr = nil
		return 0, err
	}
	return 0, io.EOF
}

func (*groupAllowlistTerminalBody) Close() error { return nil }

func TestGroupModelAllowlistLenientJSONBodyCompatibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const body = `{"model":"gpt-5.4","input":"ok"}`
	const controlBody = "{\"model\":\"gpt-5.4\",\"input\":\"line\nbreak\"}"
	for _, test := range []struct {
		name          string
		contentType   string
		body          string
		gzip          bool
		missingFooter bool
		terminalErr   error
		limit         int64
		wantStatus    int
		wantBody      string
	}{
		{name: "complete JSON transport EOF", contentType: "application/json", body: body, terminalErr: io.ErrUnexpectedEOF, wantStatus: http.StatusOK, wantBody: body},
		{name: "denied model remains denied after EOF recovery", contentType: "application/json", body: `{"model":"gpt-denied"}`, terminalErr: io.ErrUnexpectedEOF, wantStatus: http.StatusNotFound},
		{name: "JSON family control character normalization", contentType: "application/vnd.client+json; charset=utf-8", body: controlBody, terminalErr: io.ErrUnexpectedEOF, wantStatus: http.StatusOK, wantBody: `{"model":"gpt-5.4","input":"line\u000abreak"}`},
		{name: "truncated JSON is not recovered", contentType: "application/json", body: `{"model":"gpt-5.4","input":"unfinished`, terminalErr: io.ErrUnexpectedEOF, wantStatus: http.StatusBadRequest},
		{name: "joined non EOF error is not recovered", contentType: "application/json", body: body, terminalErr: errors.Join(io.ErrUnexpectedEOF, errors.New("synthetic read failure")), wantStatus: http.StatusBadRequest},
		{name: "complete gzip JSON transport EOF", contentType: "application/json", body: body, gzip: true, terminalErr: io.ErrUnexpectedEOF, wantStatus: http.StatusOK, wantBody: body},
		{name: "complete gzip JSON missing footer", contentType: "application/json", body: body, gzip: true, missingFooter: true, wantStatus: http.StatusOK, wantBody: body},
		{name: "multipart EOF stays strict", contentType: "multipart/form-data; boundary=test", body: body, terminalErr: io.ErrUnexpectedEOF, wantStatus: http.StatusBadRequest},
		{name: "binary EOF stays strict", contentType: "application/octet-stream", body: body, terminalErr: io.ErrUnexpectedEOF, wantStatus: http.StatusBadRequest},
		{name: "normalized JSON expansion is bounded", contentType: "application/json", body: controlBody, limit: int64(len(controlBody)), wantStatus: http.StatusRequestEntityTooLarge},
		{name: "decoded binary expansion is bounded", contentType: "application/octet-stream", body: body, gzip: true, limit: int64(len(body) - 1), wantStatus: http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			limit := test.limit
			if limit == 0 {
				limit = 1024
			}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(ContextKeyAPIKey), allowlistAPIKey(true, "gpt-5.4"))
				c.Next()
			})
			router.Use(GroupModelAllowlist(limit))
			handlerCalled := false
			router.POST("/v1/responses", func(c *gin.Context) {
				handlerCalled = true
				if _, ok := c.Request.Body.(*httputil.PrereadBody); !ok {
					t.Error("allowlist must restore the body with the shared zero-copy wrapper")
				}
				got, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
				if err != nil || string(got) != test.wantBody {
					t.Errorf("handler body=%q err=%v, want %q", got, err, test.wantBody)
				}
				if c.Request.ContentLength != int64(len(got)) || c.GetHeader("Content-Encoding") != "" {
					t.Error("normalized request metadata must match the restored bytes")
				}
				c.Status(http.StatusOK)
			})

			wireBody := []byte(test.body)
			if test.gzip {
				var compressed bytes.Buffer
				writer := gzip.NewWriter(&compressed)
				if _, err := writer.Write(wireBody); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				wireBody = compressed.Bytes()
				if test.missingFooter {
					wireBody = wireBody[:len(wireBody)-8]
				}
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(""))
			req.Body = &groupAllowlistTerminalBody{data: wireBody, terminalErr: test.terminalErr}
			req.ContentLength = int64(len(wireBody))
			req.Header.Set("Content-Type", test.contentType)
			if test.gzip {
				req.Header.Set("Content-Encoding", "gzip")
			}
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			if res.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s, want %d", res.Code, res.Body.String(), test.wantStatus)
			}
			if handlerCalled != (test.wantStatus == http.StatusOK) {
				t.Fatalf("handler called=%v for status=%d", handlerCalled, res.Code)
			}
		})
	}
}
