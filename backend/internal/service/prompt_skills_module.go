package service

import (
	"context"
	"sync"

	promptpolicy "github.com/Wei-Shaw/sub2api/internal/promptskills/policy"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

// promptSkills answers the prompt application and skill registry policy
// operations in process. The prompt-skills plugin served them before.
var promptSkills = sync.OnceValue(promptpolicy.New)

// invokePromptSkills runs one prompt or skill registry operation. Tests replace
// it to model per-account decisions or restricted registry profiles.
var invokePromptSkills = func(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
	return promptSkills().Invoke(ctx, in)
}

// promptPlanInvoke adapts invokePromptSkills to the account-scoped invoker of
// the prompt application path.
func promptPlanInvoke(ctx context.Context, _, _ string, in extensionv1.Invocation) (extensionv1.Result, error) {
	return invokePromptSkills(ctx, in)
}
