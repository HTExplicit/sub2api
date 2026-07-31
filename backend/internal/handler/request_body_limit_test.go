package handler

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestBodyLimitTooLarge(t *testing.T) {
	gin.SetMode(gin.TestMode)

	limit := int64(16)
	router := gin.New()
	router.Use(middleware.RequestBodyLimit(limit))
	router.POST("/test", func(c *gin.Context) {
		_, err := io.ReadAll(c.Request.Body)
		if err != nil {
			if maxErr, ok := extractMaxBytesError(err); ok {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{
					"error": buildBodyTooLargeMessage(maxErr.Limit),
				})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "read_failed",
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	payload := bytes.Repeat([]byte("a"), int(limit+1))
	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(payload))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
	require.Contains(t, recorder.Body.String(), buildBodyTooLargeMessage(limit))
}

func TestRequestBodyReadDiagnosticsAreContentFree(t *testing.T) {
	err := &pkghttputil.RequestBodyReadError{
		Stage:         "read",
		ReceivedBytes: 127,
		DecodedBytes:  127,
		Err:           io.ErrUnexpectedEOF,
	}

	require.Equal(t, "unexpected_eof", classifyRequestBodyReadError(err))
	stage, receivedBytes, decodedBytes := requestBodyReadMetrics(err)
	require.Equal(t, "read", stage)
	require.Equal(t, int64(127), receivedBytes)
	require.Equal(t, int64(127), decodedBytes)
	require.True(t, errors.Is(err, io.ErrUnexpectedEOF))
}

func TestClassifyRequestContentEncoding(t *testing.T) {
	require.Equal(t, "identity", classifyRequestContentEncoding(""))
	require.Equal(t, "identity", classifyRequestContentEncoding(" identity "))
	require.Equal(t, "zstd", classifyRequestContentEncoding("ZSTD"))
	require.Equal(t, "gzip", classifyRequestContentEncoding("x-gzip"))
	require.Equal(t, "deflate", classifyRequestContentEncoding("deflate"))
	require.Equal(t, "other", classifyRequestContentEncoding("private-value"))
}
