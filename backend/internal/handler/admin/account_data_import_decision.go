package admin

import (
	"context"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	dataImportActionReject = "reject"

	dataImportCodeCreate           = service.AccountImportCodeCreate
	dataImportCodeUpdate           = service.AccountImportCodeUpdate
	dataImportCodePayloadInvalid   = service.AccountImportCodePayloadInvalid
	dataImportCodeIdentityConflict = service.AccountImportCodeIdentityConflict
	dataImportCodeExecutionFailed  = service.AccountImportCodeExecutionFailed
)

type dataImportDecision struct {
	Account   DataAccount
	Action    string
	AccountID *int64
	Code      string
	Message   string
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
	var identityIndex *dataIdentityIndex
	if previewState, prepared := dataImportPreviewStateFromContext(ctx); prepared {
		identityIndex = previewState.identityIndex
	} else {
		existing, err := h.listAccountsFiltered(ctx, "", "", "", "", 0, "", "id", "asc")
		if err != nil {
			return preview, nil, err
		}
		identityIndex = buildDataIdentityIndex(existing)
	}

	request := extensionv1.AccountImportPlanningRequest{Items: make([]extensionv1.AccountImportItemFacts, len(req.Data.Accounts))}
	decisions := make([]dataImportDecision, len(req.Data.Accounts))
	for index := range req.Data.Accounts {
		item := req.Data.Accounts[index]
		enrichCredentialsFromIDToken(&item)
		decision := dataImportDecision{Account: item}
		facts := extensionv1.AccountImportItemFacts{PayloadValid: validateDataAccountV2(item) == nil}
		keys := dataAccountIdentityKeys(item.Platform, item.Credentials, item.Extra)
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
