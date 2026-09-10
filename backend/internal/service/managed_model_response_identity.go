package service

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// managedModelResponseContext tolerates response consumers used without an
// HTTP request, such as existing adapter unit tests. They remain unmanaged.
func managedModelResponseContext(c *gin.Context) context.Context {
	if c == nil || c.Request == nil {
		return nil
	}
	return c.Request.Context()
}

// managedModelResponseModel is only a client-visible identity projection. The
// caller must keep its routing, upstream and accounting model names separate.
func managedModelResponseModel(ctx context.Context, fallback string) string {
	request, managed := ManagedModelRequestFromContext(ctx)
	if !managed || request.Version != ManagedModelRoutesVersion ||
		strings.TrimSpace(request.Route.PublicModel) == "" || IsManagedModelSelector(request.Route.PublicModel) {
		return fallback
	}
	return request.Route.PublicModel
}

// managedModelResponseJSON replaces protocol-owned model fields, not arbitrary
// occurrences of an upstream name. Targeted JSON edits preserve user content,
// opaque values, usage precision and the remaining response bytes. An optional
// SSE event name identifies envelopes only when the payload has no type field;
// it is never inserted into the payload or preferred over an existing type.
func managedModelResponseJSON(ctx context.Context, body []byte, eventType ...string) []byte {
	publicModel := managedModelResponseModel(ctx, "")
	if publicModel == "" || !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return body
	}
	paths := []string{"model"}
	payloadType := gjson.GetBytes(body, "type")
	resolvedType := payloadType.String()
	if !payloadType.Exists() && len(eventType) > 0 {
		resolvedType = eventType[0]
	}
	switch {
	case resolvedType == "message_start":
		paths = append(paths, "message.model")
	case strings.HasPrefix(resolvedType, "response."):
		paths = append(paths, "response.model")
	}
	patched := body
	for _, path := range paths {
		model := gjson.GetBytes(patched, path)
		if model.Type != gjson.String || model.String() == publicModel {
			continue
		}
		next, err := sjson.SetBytes(patched, path, publicModel)
		if err != nil {
			return body
		}
		patched = next
	}
	return patched
}

// managedModelResponseSSELine leaves the SSE framing untouched, including the
// original whitespace and line ending. Non-data fields and [DONE] are no-ops.
func managedModelResponseSSELine(ctx context.Context, line string, eventType ...string) string {
	const prefix = "data:"
	if !strings.HasPrefix(line, prefix) {
		return line
	}
	return prefix + string(managedModelResponseJSON(ctx, []byte(line[len(prefix):]), eventType...))
}

// managedModelResponseSSEBody is for already buffered SSE bodies or constructed
// frames. Streaming callers continue to use their existing line writer.
func managedModelResponseSSEBody(ctx context.Context, body string) string {
	if managedModelResponseModel(ctx, "") == "" {
		return body
	}
	lines := strings.SplitAfter(body, "\n")
	eventType := ""
	for i, line := range lines {
		fieldLine := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		switch {
		case fieldLine == "" || fieldLine == "event":
			eventType = ""
		case strings.HasPrefix(fieldLine, "event:"):
			// SSE removes at most one optional space after the field colon.
			eventType = strings.TrimPrefix(fieldLine[len("event:"):], " ")
		}
		lines[i] = managedModelResponseSSELine(ctx, line, eventType)
	}
	return strings.Join(lines, "")
}
