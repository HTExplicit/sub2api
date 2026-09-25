package policy

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

type Module struct{}

func New() *Module { return &Module{} }
func (m *Module) ValidateConfig(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil || len(fields) != 0 {
		return nil, errors.New("account tools has no additional configuration")
	}
	return json.RawMessage(`{}`), nil
}
func (m *Module) ApplyConfig(ctx context.Context, raw json.RawMessage) error {
	_, err := m.ValidateConfig(ctx, raw)
	return err
}
func (m *Module) Status(context.Context) (json.RawMessage, error) {
	return json.Marshal(map[string]any{"taxonomy": true, "reasoning_selection": true, "text_prompt": true})
}

func failure(code, message string, status int) extensionv1.Result {
	return extensionv1.Result{Code: code, Message: message, HTTPStatus: status}
}
func strictIDs(ids []int64) bool {
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func (m *Module) Invoke(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
	if err := ctx.Err(); err != nil {
		return extensionv1.Result{}, err
	}
	if in.Capability != extensionv1.CapabilityAdmin {
		return extensionv1.Result{}, errors.New("unsupported account tools capability")
	}
	var output any = map[string]bool{"valid": true}
	switch in.Operation {
	case "import.plan":
		var request extensionv1.AccountImportPlanningRequest
		if json.Unmarshal(in.Payload, &request) != nil {
			return extensionv1.Result{}, errors.New("invalid import facts")
		}
		if (request.Phase != "" && request.Phase != "prepare" && request.Phase != "finalize") || (request.Phase == "finalize" && len(request.Prepared) != len(request.Items)) {
			return extensionv1.Result{}, errors.New("invalid import phase")
		}
		output = planImport(request)
	case "test.prompt":
		var request extensionv1.TextPromptSelection
		if json.Unmarshal(in.Payload, &request) != nil {
			return extensionv1.Result{}, errors.New("invalid test prompt metadata")
		}
		if !request.ValidUTF8 || request.Characters < 0 || request.Characters > 8192 {
			return failure("ACCOUNT_TEST_PROMPT_INVALID", "test prompt must be valid UTF-8 and at most 8192 characters", 400), nil
		}
	case "tools.describe":
		raw, err := m.Status(ctx)
		return extensionv1.Result{Payload: raw}, err
	case "taxonomy.name":
		var request extensionv1.TaxonomyName
		if json.Unmarshal(in.Payload, &request) != nil {
			return extensionv1.Result{}, errors.New("invalid taxonomy name payload")
		}
		request.Name = strings.TrimSpace(request.Name)
		if !utf8.ValidString(request.Name) || request.Name == "" || len([]rune(request.Name)) > 100 || strings.ContainsRune(request.Name, '\x00') {
			return failure("ACCOUNT_TAXONOMY_NAME_INVALID", "name must contain between 1 and 100 characters", 400), nil
		}
		request.Normalized = strings.ToLower(request.Name)
		output = request
	case "taxonomy.order":
		var request extensionv1.TaxonomyOrder
		if json.Unmarshal(in.Payload, &request) != nil {
			return extensionv1.Result{}, errors.New("invalid taxonomy order payload")
		}
		if len(request.Actual) != len(request.Ordered) {
			return failure("ACCOUNT_TAXONOMY_ORDER_CHANGED", "account taxonomy changed; reload and try again", 409), nil
		}
		if !strictIDs(request.Ordered) {
			return failure("ACCOUNT_TAXONOMY_ORDER_INVALID", "ordered_ids must contain positive unique IDs", 400), nil
		}
		for _, id := range request.Actual {
			if !slices.Contains(request.Ordered, id) {
				return failure("ACCOUNT_TAXONOMY_ORDER_CHANGED", "account taxonomy changed; reload and try again", 409), nil
			}
		}
	case "taxonomy.bulk":
		var request extensionv1.TaxonomyBulkPlan
		if json.Unmarshal(in.Payload, &request) != nil {
			return extensionv1.Result{}, errors.New("invalid taxonomy bulk payload")
		}
		if (len(request.AccountIDs) > 0) == request.HasFilters {
			return failure("ACCOUNT_TAXONOMY_TARGET_INVALID", "provide exactly one of account_ids or filters", 400), nil
		}
		if !strictIDs(request.AccountIDs) {
			return failure("ACCOUNT_TAXONOMY_ACCOUNT_IDS_INVALID", "IDs must be positive and unique", 400), nil
		}
		if !strictIDs(request.TagAddIDs) || !strictIDs(request.TagRemoveIDs) {
			return failure("ACCOUNT_TAXONOMY_TAG_IDS_INVALID", "IDs must be positive and unique", 400), nil
		}
		for _, id := range request.TagAddIDs {
			if slices.Contains(request.TagRemoveIDs, id) {
				return failure("ACCOUNT_TAXONOMY_TAG_OPERATION_CONFLICT", "a tag cannot be added and removed in the same request", 400), nil
			}
		}
		switch request.FolderAction {
		case "":
			if request.FolderID != nil {
				return failure("ACCOUNT_TAXONOMY_FOLDER_ACTION_INVALID", "folder_id requires folder_action=set", 400), nil
			}
		case "set":
			if request.FolderID == nil || *request.FolderID <= 0 {
				return failure("ACCOUNT_TAXONOMY_FOLDER_ID_INVALID", "folder_id must be positive when folder_action=set", 400), nil
			}
		case "clear":
			if request.FolderID != nil {
				return failure("ACCOUNT_TAXONOMY_FOLDER_ACTION_INVALID", "folder_id must be omitted when folder_action=clear", 400), nil
			}
		default:
			return failure("ACCOUNT_TAXONOMY_FOLDER_ACTION_INVALID", "folder_action must be set, clear, or omitted", 400), nil
		}
		if request.FolderAction == "" && len(request.TagAddIDs) == 0 && len(request.TagRemoveIDs) == 0 {
			return failure("ACCOUNT_TAXONOMY_OPERATION_REQUIRED", "at least one taxonomy operation is required", 400), nil
		}
		if request.HasFilters && (request.ExpectedMatchCount == nil || *request.ExpectedMatchCount < 0) {
			return failure("ACCOUNT_TAXONOMY_EXPECTED_COUNT_REQUIRED", "expected_match_count is required for filter targets", 400), nil
		}
	case "taxonomy.assignment":
		var request extensionv1.TaxonomyAssignmentPlan
		if json.Unmarshal(in.Payload, &request) != nil {
			return extensionv1.Result{}, errors.New("invalid taxonomy assignment payload")
		}
		if request.FolderID != nil && *request.FolderID <= 0 {
			return failure("ACCOUNT_FOLDER_ID_INVALID", "folder_id must be positive or null", 400), nil
		}
		seen := map[int64]bool{}
		tags := make([]int64, 0, len(request.TagIDs))
		for _, id := range request.TagIDs {
			if id <= 0 {
				return failure("ACCOUNT_TAG_ID_INVALID", "tag_ids must contain positive IDs", 400), nil
			}
			if !seen[id] {
				seen[id] = true
				tags = append(tags, id)
			}
		}
		request.TagIDs = tags
		output = request
	case "taxonomy.delete":
		var request struct {
			ID int64 `json:"id"`
		}
		if json.Unmarshal(in.Payload, &request) != nil || request.ID <= 0 {
			return failure("ACCOUNT_TAXONOMY_ID_INVALID", "ID must be positive", 400), nil
		}
	case "test.reasoning":
		var request extensionv1.ReasoningSelection
		if json.Unmarshal(in.Payload, &request) != nil {
			return extensionv1.Result{}, errors.New("invalid reasoning selection")
		}
		if request.Effort != "" {
			if request.Effort != strings.TrimSpace(request.Effort) || len(request.Effort) > 32 || (request.Mode != "" && request.Mode != "default" && request.Mode != "text") {
				return failure("TEST_REASONING_MODE_UNSUPPORTED", "reasoning effort is unsupported for this test mode", 400), nil
			}
			if !slices.Contains(request.Levels, request.Effort) {
				return failure("TEST_REASONING_EFFORT_UNSUPPORTED", "reasoning effort is not supported by the selected account model", 400), nil
			}
		}
	default:
		return extensionv1.Result{}, errors.New("unsupported account tools operation")
	}
	raw, err := json.Marshal(output)
	return extensionv1.Result{Payload: raw}, err
}
