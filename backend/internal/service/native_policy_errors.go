package service

import "errors"

// Retain existing error identities for the built-in policy callers and API contracts.
var ErrExtensionOperationDisabled = errors.New("extension operation is not enabled")
var ErrExtensionOperationUnavailable = errors.New("enabled extension operation is unavailable")
