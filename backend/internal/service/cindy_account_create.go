package service

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrAccountCreateInvalid        = infraerrors.BadRequest("ACCOUNT_CREATE_INVALID_REQUEST", "invalid provider create input")
	ErrAccountCreateUnavailable    = infraerrors.Conflict("ACCOUNT_CREATE_UNAVAILABLE", "provider creation is unavailable or has changed")
	ErrAccountCreateGroupsRequired = infraerrors.BadRequest("ACCOUNT_CREATE_GROUP_REQUIRED", "provider account requires an effective group")
)
var accountCreateDefaultTargets = []string{"concurrency", "priority", "rate_multiplier", "load_factor", "responses_mode"}

type accountCreateContextKey struct{}
type BoundAccountCreate struct {
	MinimumEffectiveGroups int
	runtime                *cindyProviderRuntime
	request                *extensionv1.ProviderCreateRequestV1
}

func bindProcessAccountCreate(ctx context.Context, platform, accountType, profile string, request *extensionv1.ProviderCreateRequestV1) (context.Context, func(), error) {
	if platform != PlatformCindy || accountType != AccountTypeAPIKey || profile != ProviderProfileCindyLaxaV1 {
		return nil, nil, ErrAccountCreateInvalid
	}
	if _, bound := AccountCreateFromContext(ctx); bound {
		return ctx, func() {}, ValidateAccountCreateFresh(ctx)
	}
	if request != nil {
		for key, value := range request.Values {
			if key != "device_id" || len(value) > 64 {
				return nil, nil, ErrAccountCreateInvalid
			}
		}
		seen := map[string]bool{}
		for _, target := range request.InheritDefaults {
			if !slices.Contains(accountCreateDefaultTargets, target) || seen[target] {
				return nil, nil, ErrAccountCreateInvalid
			}
			seen[target] = true
		}
	}
	cindyProviderPolicyMu.RLock()
	var once sync.Once
	release := func() { once.Do(cindyProviderPolicyMu.RUnlock) }
	create := &BoundAccountCreate{MinimumEffectiveGroups: 1, runtime: cindyProvider.Load(), request: request}
	return context.WithValue(ctx, accountCreateContextKey{}, create), release, nil
}

func ValidateAccountCreateFresh(ctx context.Context) error {
	create, bound := AccountCreateFromContext(ctx)
	if !bound {
		return nil
	}
	if ctx.Err() != nil || create.runtime != cindyProvider.Load() {
		return ErrAccountCreateUnavailable
	}
	return nil
}
func AccountCreateFromContext(ctx context.Context) (*BoundAccountCreate, bool) {
	if ctx == nil {
		return nil, false
	}
	create, ok := ctx.Value(accountCreateContextKey{}).(*BoundAccountCreate)
	return create, ok && create != nil
}

func retainAccountCreateUntilTransactionEnds(ctx context.Context, tx *dbent.Tx, release func()) {
	var once sync.Once
	finish := func() { once.Do(release) }
	tx.OnCommit(func(next dbent.Committer) dbent.Committer {
		return dbent.CommitFunc(func(commitCtx context.Context, transaction *dbent.Tx) error {
			if err := ValidateAccountCreateFresh(ctx); err != nil {
				return err // rollback owns release when commit is rejected
			}
			err := next.Commit(commitCtx, transaction)
			if err == nil {
				finish()
			}
			return err
		})
	})
	tx.OnRollback(func(next dbent.Rollbacker) dbent.Rollbacker {
		return dbent.RollbackFunc(func(rollbackCtx context.Context, transaction *dbent.Tx) error {
			err := next.Rollback(rollbackCtx, transaction)
			finish()
			return err
		})
	})
}

func accountCreateValueExplicit(input *CreateAccountInput, target string) bool {
	if input.ExplicitCreateFields != nil {
		return input.ExplicitCreateFields[target]
	}
	// Direct Go callers can supply the presence map for explicit zero/null.
	// Existing untagged callers never enter numeric inheritance at all.
	switch target {
	case "concurrency":
		return input.Concurrency != 0
	case "priority":
		return input.Priority != 0
	case "rate_multiplier":
		return input.RateMultiplier != nil
	case "load_factor":
		return input.LoadFactor != nil
	}
	return false
}

func applyAccountCreateProfile(ctx context.Context, input *CreateAccountInput) error {
	create, bound := AccountCreateFromContext(ctx)
	if !bound || ValidateAccountCreateFresh(ctx) != nil {
		return ErrAccountCreateUnavailable
	}
	definition := struct {
		Defaults struct {
			Concurrency    int
			Priority       int
			RateMultiplier float64
			LoadFactor     *int
			ResponsesMode  string
		}
	}{}
	definition.Defaults.Concurrency = 3
	definition.Defaults.Priority = 50
	definition.Defaults.RateMultiplier = 1
	definition.Defaults.ResponsesMode = "force_responses"
	if input.ProbeEnabled != nil && *input.ProbeEnabled {
		return ErrAccountCreateInvalid
	}
	if len(input.ModelContextOverrides) != 0 {
		return ErrAccountCreateInvalid
	}
	input.Extra = maps.Clone(input.Extra)
	if input.Extra == nil {
		input.Extra = map[string]any{}
	}
	request := create.request
	if request != nil {
		for key, value := range request.Values {
			if key != "device_id" {
				return ErrAccountCreateInvalid
			}
			value = strings.TrimSpace(value)
			if existing, present := input.Extra[CindyDeviceIDExtraKey]; present {
				text, valid := existing.(string)
				if !valid || strings.TrimSpace(text) != value {
					return ErrAccountCreateInvalid
				}
			}
			if value != "" {
				input.Extra[CindyDeviceIDExtraKey] = value
			} else {
				delete(input.Extra, CindyDeviceIDExtraKey)
			}
		}
		for _, target := range request.InheritDefaults {
			if accountCreateValueExplicit(input, target) {
				continue
			}
			switch target {
			case "concurrency":
				input.Concurrency = definition.Defaults.Concurrency
			case "priority":
				input.Priority = definition.Defaults.Priority
			case "rate_multiplier":
				value := definition.Defaults.RateMultiplier
				input.RateMultiplier = &value
			case "load_factor":
				input.LoadFactor = nil
				if definition.Defaults.LoadFactor != nil {
					value := *definition.Defaults.LoadFactor
					input.LoadFactor = &value
				}
			}
		}
	}
	if mode, present := input.Extra[CindyResponsesModeExtraKey]; present {
		text, valid := mode.(string)
		if !valid || !slices.Contains([]string{"auto", "force_responses", "force_chat_completions"}, text) {
			return ErrAccountCreateInvalid
		}
	} else {
		input.Extra[CindyResponsesModeExtraKey] = definition.Defaults.ResponsesMode
	}
	// Creation derives provenance from the actual input, not a client assertion.
	// Keep the legacy input validator even though valid claims are not trusted.
	if source, present := input.Extra[CindyDeviceIDSourceExtraKey]; present {
		if _, valid := normalizeCindyDeviceIDSource(source); !valid {
			return infraerrors.BadRequest("CINDY_DEVICE_ID_SOURCE_INVALID", "cindy_device_id_source is invalid")
		}
	}
	delete(input.Extra, CindyDeviceIDSourceExtraKey)
	return nil
}
