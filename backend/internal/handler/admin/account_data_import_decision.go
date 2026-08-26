package admin

import (
	"context"
	"errors"
	"sort"
	"strings"

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
	existing, err := h.listAccountsFiltered(ctx, "", "", "", "", 0, "", "id", "asc")
	if err != nil {
		return preview, nil, err
	}
	identityIndex := buildDataIdentityIndex(existing)
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
		targetGroup, err = h.adminService.GetGroup(ctx, *req.TargetGroupID)
		if err != nil && !errors.Is(err, service.ErrGroupNotFound) {
			return preview, nil, err
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
	// A canonical Cindy group with no members is the only safe bootstrap target:
	// the repository's strict classifier intentionally returns false for empty
	// groups, while the first import needs one empty target to establish that
	// membership. Once any member exists, retain the full strict all-members gate.
	targetIsStrict := targetHasCanonicalIdentity &&
		((targetGroup.StrictCindyKnown && targetGroup.StrictCindy) || !targetHasMember)

	decisions := make([]dataImportDecision, len(req.Data.Accounts))
	fingerprintItems := make(map[string][]int)
	deviceItems := make(map[string][]int)
	for index := range req.Data.Accounts {
		item := req.Data.Accounts[index]
		enrichCredentialsFromIDToken(&item)
		legacyCindy := service.IsLegacyCindyAPIKeyAccount(item.Platform, item.Type, item.Credentials)
		cindyCandidate := legacyCindy || service.IsCindyAPIKeyAccount(item.Platform, item.Type, item.Credentials)
		decision := dataImportDecision{Account: item}
		if cindyCandidate {
			apiKey, ok := item.Credentials["api_key"].(string)
			if !ok || strings.TrimSpace(apiKey) == "" {
				rejectDataImportDecision(&decision, dataImportCodeCindyAPIKeyInvalid)
			}
		}
		if legacyCindy && !decision.rejected() {
			decision.Account.Platform = service.PlatformCindy
		}
		item = decision.Account
		isCindy := service.IsCindyAPIKeyAccount(item.Platform, item.Type, item.Credentials)
		if isCindy {
			decision.Account.Groups = nil
			if decision.rejected() {
				// Preserve the earlier credential validation result.
			} else if req.TargetGroupID == nil || *req.TargetGroupID <= 0 {
				rejectDataImportDecision(&decision, dataImportCodeCindyTargetRequired)
			} else if !targetIsStrict {
				rejectDataImportDecision(&decision, dataImportCodeCindyTargetInvalid)
			} else {
				decision.GroupIDs = []int64{*req.TargetGroupID}
			}
		}
		if !decision.rejected() {
			if validateErr := validateDataAccountV2(decision.Account); validateErr != nil {
				rejectDataImportDecision(&decision, dataImportCodePayloadInvalid)
			}
		}
		if isCindy && !decision.rejected() {
			if rawDevice, present := item.Extra[service.CindyDeviceIDExtraKey]; present {
				deviceID, ok := rawDevice.(string)
				deviceID = strings.TrimSpace(deviceID)
				if !ok || !service.ValidCindyDeviceID(deviceID) {
					rejectDataImportDecision(&decision, dataImportCodeCindyDeviceInvalid)
				} else {
					decision.deviceID = deviceID
				}
			}
			if rawSource, present := item.Extra[service.CindyDeviceIDSourceExtraKey]; present &&
				!service.ValidCindyDeviceIDSource(rawSource) {
				rejectDataImportDecision(&decision, dataImportCodeCindyDeviceInvalid)
			}
			if !decision.rejected() {
				if decision.deviceID != "" {
					deviceItems[decision.deviceID] = append(deviceItems[decision.deviceID], index)
				}
				keys := dataAccountIdentityKeys(item.Platform, item.Credentials, item.Extra)
				for _, key := range keys {
					if key.Label == "credential_fingerprint" {
						fingerprintItems[key.Value] = append(fingerprintItems[key.Value], index)
						break
					}
				}
			}
		}
		if !decision.rejected() {
			matches := identityIndex.Find(dataAccountIdentityKeys(item.Platform, item.Credentials, item.Extra))
			switch len(matches) {
			case 0:
				decision.Action, decision.Code = dataImportActionCreate, dataImportCodeCreate
			case 1:
				id := matches[0].AccountID
				decision.Action, decision.Code, decision.AccountID = dataImportActionUpdate, dataImportCodeUpdate, &id
			default:
				if isCindy {
					rejectDataImportDecision(&decision, dataImportCodeCindyCredentialConflict)
				} else {
					rejectDataImportDecision(&decision, dataImportCodeIdentityConflict)
				}
			}
			if !decision.rejected() {
				decision.Message = dataImportMessage(decision.Code)
			}
		}
		decisions[index] = decision
	}

	for _, indexes := range fingerprintItems {
		if len(indexes) < 2 {
			continue
		}
		for _, index := range indexes {
			rejectDataImportDecision(&decisions[index], dataImportCodeCindyCredentialConflict)
		}
	}
	for deviceID, indexes := range deviceItems {
		if len(indexes) > 1 {
			for _, index := range indexes {
				rejectDataImportDecision(&decisions[index], dataImportCodeCindyDeviceConflict)
			}
			continue
		}
		index := indexes[0]
		owners := deviceOwners[deviceID]
		if len(owners) == 0 {
			continue
		}
		decisionID := int64(0)
		if decisions[index].AccountID != nil {
			decisionID = *decisions[index].AccountID
		}
		if len(owners) != 1 || owners[0] != decisionID {
			rejectDataImportDecision(&decisions[index], dataImportCodeCindyDeviceConflict)
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
