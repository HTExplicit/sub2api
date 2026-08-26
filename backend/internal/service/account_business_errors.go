package service

import "strings"

const (
	AccountJobCodePayloadExpired         = "payload_expired"
	AccountJobCodePayloadUnavailable     = "payload_unavailable"
	AccountJobCodeCancelCheckFailed      = "cancel_check_failed"
	AccountJobCodeReservationFailed      = "item_reservation_failed"
	AccountJobCodeCompletionFailed       = "item_completion_failed"
	AccountJobCodeResultRedacted         = "result_redacted"
	AccountJobCodeExecutionFailed        = "execution_failed"
	AccountJobCodeCindyTargetChanged     = "cindy_cleanup_target_changed"
	AccountJobCodeCindyCleanupFailed     = "cindy_cleanup_failed"
	AccountImportCodeCreate              = "account_import_create"
	AccountImportCodeUpdate              = "account_import_update"
	AccountImportCodePayloadInvalid      = "account_import_payload_invalid"
	AccountImportCodeIdentityConflict    = "account_import_identity_conflict"
	AccountImportCodeCindyTargetRequired = "cindy_import_target_group_required"
	AccountImportCodeCindyTargetInvalid  = "cindy_import_target_group_invalid"
	AccountImportCodeCindyAPIKeyInvalid  = "cindy_import_api_key_invalid"
	AccountImportCodeCredentialConflict  = "cindy_import_credential_conflict"
	AccountImportCodeDeviceConflict      = "cindy_import_device_conflict"
	AccountImportCodeDeviceInvalid       = "cindy_import_device_invalid"
	AccountImportCodeExecutionFailed     = "account_import_execution_failed"
)

type accountBusinessMessage struct {
	message string
	failure bool
}

var accountBusinessMessageCatalog = map[string]accountBusinessMessage{
	AccountJobCodePayloadExpired:         {message: "account job payload expired", failure: true},
	AccountJobCodePayloadUnavailable:     {message: "account job payload is unavailable", failure: true},
	AccountJobCodeCancelCheckFailed:      {message: "account job cancellation state is unavailable", failure: true},
	AccountJobCodeReservationFailed:      {message: "account job items could not be reserved", failure: true},
	AccountJobCodeCompletionFailed:       {message: "account job item result could not be persisted", failure: true},
	AccountJobCodeResultRedacted:         {message: "account job result was rejected", failure: true},
	AccountJobCodeExecutionFailed:        {message: "account job item failed", failure: true},
	AccountJobCodeCindyTargetChanged:     {message: "matching Cindy accounts changed; reload and confirm again", failure: true},
	AccountJobCodeCindyCleanupFailed:     {message: "Cindy account cleanup failed", failure: true},
	AccountImportCodeCreate:              {message: "account will be created"},
	AccountImportCodeUpdate:              {message: "account will be updated"},
	AccountImportCodePayloadInvalid:      {message: "account import item is invalid", failure: true},
	AccountImportCodeIdentityConflict:    {message: "account identity matches multiple existing accounts", failure: true},
	AccountImportCodeCindyTargetRequired: {message: "one explicit target group is required for Cindy imports", failure: true},
	AccountImportCodeCindyTargetInvalid:  {message: "target group is not a strict Cindy group", failure: true},
	AccountImportCodeCindyAPIKeyInvalid:  {message: "Cindy API key is required", failure: true},
	AccountImportCodeCredentialConflict:  {message: "credential is duplicated in the submitted import", failure: true},
	AccountImportCodeDeviceConflict:      {message: "device identity belongs to another Cindy credential", failure: true},
	AccountImportCodeDeviceInvalid:       {message: "Cindy device identity is invalid", failure: true},
	AccountImportCodeExecutionFailed:     {message: "account import item could not be applied", failure: true},
	"target_missing":                     {message: "account job target is missing", failure: true},
	"delete_failed":                      {message: "account could not be deleted", failure: true},
	"clear_error_failed":                 {message: "account error state could not be cleared", failure: true},
	"account_not_found":                  {message: "account was not found", failure: true},
	"refresh_failed":                     {message: "account refresh failed", failure: true},
	"payload_invalid":                    {message: "account job payload is invalid", failure: true},
	"create_failed":                      {message: "account could not be created", failure: true},
	"credentials_update_failed":          {message: "account credentials could not be updated", failure: true},
	"duplicate_review_failed":            {message: "duplicate account review failed", failure: true},
	"duplicate_merge_failed":             {message: "duplicate account merge failed", failure: true},
	"kind_unsupported":                   {message: "account job kind is unsupported", failure: true},
	"filters_invalid":                    {message: "account job filters are invalid", failure: true},
	"bulk_update_failed":                 {message: "account bulk update failed", failure: true},
	"taxonomy_target_changed":            {message: "account taxonomy target changed", failure: true},
	"taxonomy_unavailable":               {message: "account taxonomy is unavailable", failure: true},
	"taxonomy_update_failed":             {message: "account taxonomy update failed", failure: true},
	"account_list_failed":                {message: "account list is unavailable", failure: true},
	"refresh_tier_failed":                {message: "account tier refresh failed", failure: true},
	"refresh_tier_update_failed":         {message: "refreshed account tier could not be saved", failure: true},
	"import_failed":                      {message: "account import failed", failure: true},
}

// AccountBusinessMessage returns a fixed, credential-safe business message.
// Callers must not fall back to raw errors when the code is unknown.
func AccountBusinessMessage(code string) (string, bool) {
	entry, ok := accountBusinessMessageCatalog[strings.TrimSpace(code)]
	return entry.message, ok
}

// NormalizeAccountBusinessFailure admits only catalog entries that are safe
// failure results. Preview success codes are deliberately rejected here.
func NormalizeAccountBusinessFailure(code string) (string, string) {
	code = strings.TrimSpace(code)
	if entry, ok := accountBusinessMessageCatalog[code]; ok && entry.failure {
		return code, entry.message
	}
	fallback := accountBusinessMessageCatalog[AccountJobCodeExecutionFailed]
	return AccountJobCodeExecutionFailed, fallback.message
}
