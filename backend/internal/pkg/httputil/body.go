package httputil

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// RequestBodyReadError records where request-body consumption failed without
// retaining or exposing the request content itself.
type RequestBodyReadError struct {
	Stage         string
	ReceivedBytes int64
	DecodedBytes  int64
	Err           error
}

func (e *RequestBodyReadError) Error() string {
	return fmt.Sprintf(
		"%s request body after %d received bytes and %d decoded bytes: %v",
		e.Stage,
		e.ReceivedBytes,
		e.DecodedBytes,
		e.Err,
	)
}

func (e *RequestBodyReadError) Unwrap() error {
	return e.Err
}

const (
	requestBodyReadInitCap    = 512
	requestBodyReadMaxInitCap = 1 << 20
	jsonUTF8BOMLen            = 3
	// maxDecompressedBodySize limits the decompressed request body to 64 MB
	// to prevent decompression bomb attacks.
	maxDecompressedBodySize = 64 << 20
)

// ReadRequestBodyWithPrealloc reads request body with preallocated buffer based
// on content length, transparently decoding any Content-Encoding the upstream
// client used to compress the body (zstd, gzip, deflate).
func ReadRequestBodyWithPrealloc(req *http.Request) ([]byte, error) {
	if req == nil || req.Body == nil {
		return nil, nil
	}

	capHint := requestBodyReadInitCap
	if req.ContentLength > 0 {
		switch {
		case req.ContentLength < int64(requestBodyReadInitCap):
			capHint = requestBodyReadInitCap
		case req.ContentLength > int64(requestBodyReadMaxInitCap):
			capHint = requestBodyReadMaxInitCap
		default:
			capHint = int(req.ContentLength)
		}
	}

	buf := bytes.NewBuffer(make([]byte, 0, capHint))
	receivedBytes, readErr := io.Copy(buf, req.Body)
	raw := buf.Bytes()

	enc := strings.ToLower(strings.TrimSpace(req.Header.Get("Content-Encoding")))
	if enc == "" || enc == "identity" {
		if readErr != nil {
			return raw, &RequestBodyReadError{
				Stage:         "read",
				ReceivedBytes: receivedBytes,
				DecodedBytes:  int64(len(raw)),
				Err:           readErr,
			}
		}
		return raw, nil
	}
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return nil, &RequestBodyReadError{
			Stage:         "read",
			ReceivedBytes: receivedBytes,
			Err:           readErr,
		}
	}

	decoded, decodeErr := decompressRequestBody(enc, raw)
	if decodeErr != nil {
		wrappedDecodeErr := fmt.Errorf("decode Content-Encoding %q: %w", enc, decodeErr)
		if !errors.Is(decodeErr, io.ErrUnexpectedEOF) {
			return nil, &RequestBodyReadError{
				Stage:         "decode",
				ReceivedBytes: receivedBytes,
				DecodedBytes:  int64(len(decoded)),
				Err:           wrappedDecodeErr,
			}
		}
		combinedErr := wrappedDecodeErr
		stage := "decode"
		if readErr != nil {
			combinedErr = errors.Join(readErr, wrappedDecodeErr)
			stage = "read_decode"
		}
		updateDecodedRequestMetadata(req, len(decoded))
		return decoded, &RequestBodyReadError{
			Stage:         stage,
			ReceivedBytes: receivedBytes,
			DecodedBytes:  int64(len(decoded)),
			Err:           combinedErr,
		}
	}

	updateDecodedRequestMetadata(req, len(decoded))
	if readErr != nil {
		return decoded, &RequestBodyReadError{
			Stage:         "read",
			ReceivedBytes: receivedBytes,
			DecodedBytes:  int64(len(decoded)),
			Err:           readErr,
		}
	}

	return decoded, nil
}

// ReadLenientJSONRequestBodyWithPrealloc reads a request body and normalizes
// JSON string control bytes before strict validation.
func ReadLenientJSONRequestBodyWithPrealloc(req *http.Request, maxNormalizedBytes int64) ([]byte, error) {
	body, readErr := ReadRequestBodyWithPrealloc(req)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return nil, readErr
	}
	normalized, normalizeErr := NormalizeLenientJSONRequestBody(body, maxNormalizedBytes)
	if normalizeErr != nil {
		return nil, normalizeErr
	}
	if readErr != nil && !json.Valid(normalized) {
		return nil, readErr
	}
	return normalized, nil
}

func updateDecodedRequestMetadata(req *http.Request, decodedBytes int) {
	req.Header.Del("Content-Encoding")
	req.Header.Del("Content-Length")
	req.ContentLength = int64(decodedBytes)
}

func decompressRequestBody(encoding string, raw []byte) ([]byte, error) {
	switch encoding {
	case "zstd":
		dec, err := zstd.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer dec.Close()
		return io.ReadAll(io.LimitReader(dec, maxDecompressedBodySize))
	case "gzip", "x-gzip":
		gr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer func() { _ = gr.Close() }()
		return io.ReadAll(io.LimitReader(gr, maxDecompressedBodySize))
	case "deflate":
		zr, err := zlib.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer func() { _ = zr.Close() }()
		return io.ReadAll(io.LimitReader(zr, maxDecompressedBodySize))
	default:
		return nil, errors.New("unsupported Content-Encoding")
	}
}

// NormalizeLenientJSONRequestBody escapes raw control bytes that broken
// OpenAI-compatible clients sometimes place inside JSON strings.
func NormalizeLenientJSONRequestBody(body []byte, maxNormalizedBytes int64) ([]byte, error) {
	if maxNormalizedBytes <= 0 {
		maxNormalizedBytes = maxDecompressedBodySize
	}

	body = trimUTF8BOM(body)
	if len(body) == 0 {
		return body, nil
	}
	if int64(len(body)) > maxNormalizedBytes {
		return nil, &http.MaxBytesError{Limit: maxNormalizedBytes}
	}

	var out []byte
	inString := false
	escaped := false
	for i, b := range body {
		if inString && isJSONControlByte(b) {
			if out == nil {
				capHint := len(body) + 6
				if int64(capHint) > maxNormalizedBytes {
					capHint = int(maxNormalizedBytes)
				}
				out = make([]byte, 0, capHint)
				out = append(out, body[:i]...)
			}
			if int64(len(out)+6) > maxNormalizedBytes {
				return nil, &http.MaxBytesError{Limit: maxNormalizedBytes}
			}
			out = appendJSONUnicodeEscape(out, b)
			escaped = false
			continue
		}

		switch {
		case escaped:
			escaped = false
		case inString && b == '\\':
			escaped = true
		case b == '"':
			inString = !inString
		}

		if out != nil {
			if int64(len(out)+1) > maxNormalizedBytes {
				return nil, &http.MaxBytesError{Limit: maxNormalizedBytes}
			}
			out = append(out, b)
		}
	}
	if out != nil {
		return out, nil
	}
	return body, nil
}

func trimUTF8BOM(body []byte) []byte {
	if len(body) >= jsonUTF8BOMLen && body[0] == 0xef && body[1] == 0xbb && body[2] == 0xbf {
		return body[jsonUTF8BOMLen:]
	}
	return body
}

func isJSONControlByte(b byte) bool {
	return b < 0x20 || b == 0x7f
}

func appendJSONUnicodeEscape(dst []byte, b byte) []byte {
	const hex = "0123456789abcdef"
	return append(dst, '\\', 'u', '0', '0', hex[b>>4], hex[b&0x0f])
}
