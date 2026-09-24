package service

import (
	"context"
	"sync"

	accounttoolspolicy "github.com/Wei-Shaw/sub2api/internal/accounttools/policy"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

// accountTools answers the account classification, test and import planning
// operations in process. The account-tools plugin served them before.
var accountTools = sync.OnceValue(accounttoolspolicy.New)

// invokeAccountTools runs one account tools operation. Tests replace it to
// model rejected plans.
var invokeAccountTools = func(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
	return accountTools().Invoke(ctx, in)
}
