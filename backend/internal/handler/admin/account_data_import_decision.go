package admin

import (
	"context"
	"fmt"
	"strconv"
	"strings"

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
	// ValidationError is the concrete reason an entry failed validation, and
	// MatchedAccountIDs lists every existing account its identity matched.
	ValidationError   string
	MatchedAccountIDs []int64
}

func (d dataImportDecision) rejected() bool { return d.Action == dataImportActionReject }

func dataImportMessage(code string) string {
	if message, ok := service.AccountBusinessMessage(code); ok {
		return message
	}
	message, _ := service.AccountBusinessMessage(dataImportCodeExecutionFailed)
	return message
}

// dataImportDecisionMessage explains a decision with its own facts: the
// validation error of an invalid entry and the IDs an ambiguous identity matched.
func dataImportDecisionMessage(decision dataImportDecision) string {
	switch decision.Code {
	case dataImportCodePayloadInvalid:
		if decision.ValidationError != "" {
			return decision.ValidationError
		}
	case dataImportCodeIdentityConflict:
		if len(decision.MatchedAccountIDs) > 0 {
			ids := make([]string, 0, len(decision.MatchedAccountIDs))
			for _, id := range decision.MatchedAccountIDs {
				ids = append(ids, strconv.FormatInt(id, 10))
			}
			return fmt.Sprintf("%s: %s", dataImportMessage(decision.Code), strings.Join(ids, ", "))
		}
	}
	return dataImportMessage(decision.Code)
}

func rejectDataImportDecision(decision *dataImportDecision, code string) {
	decision.Action = dataImportActionReject
	decision.AccountID = nil
	decision.Code = code
	decision.Message = dataImportDecisionMessage(*decision)
}

func (h *AccountHandler) previewDataImport(ctx context.Context, req DataImportRequest) (DataImportPreviewResult, []dataImportDecision, error) {
	preview := DataImportPreviewResult{Items: make([]DataImportItemResult, 0, len(req.Data.Accounts))}
	if err := validateDataHeader(req.Data); err != nil {
		return preview, nil, infraerrors.BadRequest("ACCOUNT_IMPORT_PAYLOAD_INVALID", "invalid account import payload: "+err.Error())
	}
	if err := validateDataImportRequest(req); err != nil {
		return preview, nil, infraerrors.BadRequest("ACCOUNT_IMPORT_SETTINGS_INVALID", "invalid account import settings: "+err.Error())
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
		validationErr := validateDataAccountV2(item)
		if validationErr != nil {
			decision.ValidationError = validationErr.Error()
		}
		facts := extensionv1.AccountImportItemFacts{PayloadValid: validationErr == nil}
		keys := dataAccountIdentityKeys(item.Platform, item.Credentials, item.Extra)
		for _, match := range identityIndex.Find(keys) {
			decision.MatchedAccountIDs = append(decision.MatchedAccountIDs, match.AccountID)
			if len(facts.Matches) < 2 {
				facts.Matches = append(facts.Matches, match.AccountID)
			} // Two witnesses already prove ambiguity to the policy.
		}
		request.Items[index], decisions[index] = facts, decision
	}
	plans, err := service.PlanAccountImport(ctx, request)
	if err != nil {
		return preview, nil, err
	}
	for index, plan := range plans {
		decision := &decisions[index]
		decision.Action, decision.Code = plan.Action, plan.Code
		decision.Message = dataImportDecisionMessage(*decision)
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
			MatchedAccountIDs: decision.MatchedAccountIDs,
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
