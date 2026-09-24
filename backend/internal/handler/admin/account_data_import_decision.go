package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	dataImportActionReject = "reject"

	dataImportCodeCreate                  = service.AccountImportCodeCreate
	dataImportCodeUpdate                  = service.AccountImportCodeUpdate
	dataImportCodePayloadInvalid          = service.AccountImportCodePayloadInvalid
	dataImportCodeIdentityConflict        = service.AccountImportCodeIdentityConflict
	dataImportCodeCindyTargetRequired     = service.AccountImportCodeCindyTargetRequired
	dataImportCodeCindyTargetInvalid      = service.AccountImportCodeCindyTargetInvalid
	dataImportCodeCindyAPIKeyInvalid      = service.AccountImportCodeCindyAPIKeyInvalid
	dataImportCodeCindyCredentialConflict = service.AccountImportCodeCredentialConflict
	dataImportCodeCindyDeviceConflict     = service.AccountImportCodeDeviceConflict
	dataImportCodeCindyDeviceInvalid      = service.AccountImportCodeDeviceInvalid
	dataImportCodeExecutionFailed         = service.AccountImportCodeExecutionFailed
)

type dataImportDecision struct {
	Account   DataAccount
	Action    string
	AccountID *int64
	GroupIDs  []int64
	Code      string
	Message   string
	deviceID  string
}

func (d dataImportDecision) rejected() bool { return d.Action == dataImportActionReject }

func dataImportMessage(code string) string {
	if message, ok := service.AccountBusinessMessage(code); ok {
		return message
	}
	message, _ := service.AccountBusinessMessage(dataImportCodeExecutionFailed)
	return message
}

func rejectDataImportDecision(decision *dataImportDecision, code string) {
	decision.Action = dataImportActionReject
	decision.AccountID = nil
	decision.Code = code
	decision.Message = dataImportMessage(code)
}

func (h *AccountHandler) previewDataImport(ctx context.Context, req DataImportRequest) (DataImportPreviewResult, []dataImportDecision, error) {
	preview := DataImportPreviewResult{Items: make([]DataImportItemResult, 0, len(req.Data.Accounts))}
	if err := validateDataHeader(req.Data); err != nil {
		return preview, nil, infraerrors.BadRequest("ACCOUNT_IMPORT_PAYLOAD_INVALID", "invalid account import payload")
	}
	if err := validateDataImportRequest(req); err != nil {
		return preview, nil, infraerrors.BadRequest("ACCOUNT_IMPORT_SETTINGS_INVALID", "invalid account import settings")
	}
	var err error
	previewState, prepared := dataImportPreviewStateFromContext(ctx)
	var existing []service.Account
	var identityIndex *dataIdentityIndex
	if prepared {
		existing = previewState.existing
		identityIndex = previewState.identityIndex
	} else {
		existing, err = h.listAccountsFiltered(ctx, "", "", "", "", 0, "", "id", "asc")
		if err != nil {
			return preview, nil, err
		}
		identityIndex = buildDataIdentityIndex(existing)
	}
	deviceOwners := make(map[string][]int64)
	for index := range existing {
		account := &existing[index]
		if !isStrictCindyAccount(account) {
			continue
		}
		deviceID := strings.TrimSpace(account.GetExtraString(service.CindyDeviceIDExtraKey))
		if service.ValidCindyDeviceID(deviceID) {
			deviceOwners[deviceID] = append(deviceOwners[deviceID], account.ID)
		}
	}
	for deviceID := range deviceOwners {
		sort.Slice(deviceOwners[deviceID], func(i, j int) bool { return deviceOwners[deviceID][i] < deviceOwners[deviceID][j] })
	}

	var targetGroup *service.Group
	if req.TargetGroupID != nil && *req.TargetGroupID > 0 {
		if prepared {
			if group, ok := previewState.groupsByID[*req.TargetGroupID]; ok {
				groupCopy := group
				targetGroup = &groupCopy
			}
		} else {
			targetGroup, err = h.adminService.GetGroup(ctx, *req.TargetGroupID)
			if err != nil && !errors.Is(err, service.ErrGroupNotFound) {
				return preview, nil, err
			}
		}
	}
	targetHasMember := false
	if targetGroup != nil {
		for index := range existing {
			for _, groupID := range existing[index].GroupIDs {
				if groupID == targetGroup.ID {
					targetHasMember = true
					break
				}
			}
			if targetHasMember {
				break
			}
		}
	}
	targetHasCanonicalIdentity := targetGroup != nil && targetGroup.Platform == service.PlatformCindy &&
		targetGroup.EffectiveWirePlatform() == service.WirePlatformOpenAI &&
		targetGroup.EffectiveProviderProfile() == service.ProviderProfileCindyLaxaV1
	request := extensionv1.AccountImportPlanningRequest{TargetCanonical: targetHasCanonicalIdentity, TargetHasMembers: targetHasMember, Items: make([]extensionv1.AccountImportItemFacts, len(req.Data.Accounts))}
	if req.TargetGroupID != nil {
		request.TargetGroupID = *req.TargetGroupID
	}
	if targetGroup != nil {
		request.TargetStrict = targetGroup.StrictCindyKnown && targetGroup.StrictCindy
	}
	decisions := make([]dataImportDecision, len(req.Data.Accounts))
	for index := range req.Data.Accounts {
		item := req.Data.Accounts[index]
		enrichCredentialsFromIDToken(&item)
		legacy := service.IsLegacyCindyAPIKeyAccount(item.Platform, item.Type, item.Credentials)
		candidate := legacy || service.IsCindyAPIKeyAccount(item.Platform, item.Type, item.Credentials)
		key, keyIsString := item.Credentials["api_key"].(string)
		keyValid := keyIsString && strings.TrimSpace(key) != ""
		// Canonicalization is a bounded identity projection; the plugin decides
		// whether it may be used. Credentials remain in this host-only record.
		if legacy && keyValid {
			item.Platform = service.PlatformCindy
		}
		if service.IsCindyAPIKeyAccount(item.Platform, item.Type, item.Credentials) {
			item.Groups = nil
		}
		decision := dataImportDecision{Account: item}
		facts := extensionv1.AccountImportItemFacts{CindyCandidate: candidate, LegacyCindy: legacy, APIKeyValid: keyValid, PayloadValid: validateDataAccountV2(item) == nil, DeviceValid: true, DeviceSourceValid: true}
		if raw, present := item.Extra[service.CindyDeviceIDExtraKey]; present {
			device, validString := raw.(string)
			device = strings.TrimSpace(device)
			facts.DeviceValid = validString && service.ValidCindyDeviceID(device)
			if facts.DeviceValid {
				decision.deviceID = device
				digest := sha256.Sum256([]byte(device))
				facts.DeviceIdentity = hex.EncodeToString(digest[:])
				owners := deviceOwners[device]
				facts.DeviceOwners = append([]int64(nil), owners[:min(2, len(owners))]...)
			}
		}
		if raw, present := item.Extra[service.CindyDeviceIDSourceExtraKey]; present {
			facts.DeviceSourceValid = service.ValidCindyDeviceIDSource(raw)
		}
		keys := dataAccountIdentityKeys(item.Platform, item.Credentials, item.Extra)
		for _, key := range keys {
			if key.Label == "credential_fingerprint" {
				facts.CredentialIdentity = key.Value
				break
			}
		}
		for _, match := range identityIndex.Find(keys) {
			facts.Matches = append(facts.Matches, match.AccountID)
			if len(facts.Matches) == 2 {
				break
			} // Two witnesses already prove ambiguity.
		}
		request.Items[index], decisions[index] = facts, decision
	}
	plans, err := service.PlanAccountImport(ctx, request)
	if err != nil {
		return preview, nil, err
	}
	for index, plan := range plans {
		decision := &decisions[index]
		decision.Action, decision.Code, decision.Message = plan.Action, plan.Code, dataImportMessage(plan.Code)
		decision.GroupIDs = append([]int64(nil), plan.GroupIDs...)
		if plan.AccountID > 0 {
			id := plan.AccountID
			decision.AccountID = &id
		}
	}

	for index := range decisions {
		decision := decisions[index]
		preview.Items = append(preview.Items, DataImportItemResult{
			Index: index, Name: decision.Account.Name, Action: decision.Action,
			AccountID: decision.AccountID, Code: decision.Code, Message: decision.Message,
		})
		switch decision.Action {
		case dataImportActionCreate:
			preview.CreateCount++
		case dataImportActionUpdate:
			preview.UpdateCount++
		default:
			preview.RejectCount++
		}
	}
	return preview, decisions, nil
}
