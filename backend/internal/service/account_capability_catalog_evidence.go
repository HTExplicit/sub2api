package service

import (
	"encoding/json"
	"time"
)

func capabilityIsBasicTextEvidence(item AccountCapabilityItem) bool {
	if item.Kind != AccountCapabilityKindProbe || item.Profile != AccountCapabilityProfileText {
		return false
	}
	switch item.Protocol {
	case AccountCapabilityProtocolResponses, AccountCapabilityProtocolChatCompletions,
		AccountCapabilityProtocolMessages, AccountCapabilityProtocolResponsesWebSocket:
		return true
	default:
		// Token-count probes also use profile=text. They are not generation
		// evidence, and must not upgrade or downgrade a basic text result.
		return false
	}
}

func capabilityEvidenceCurrent(item AccountCapabilityItem, account *Account, fingerprint string) bool {
	return account != nil && account.ManagementFolderID != nil && item.AccountID == account.ID &&
		item.ConfigFingerprint == fingerprint && item.FolderID == *account.ManagementFolderID
}

func capabilityDiscoveryModelNames(raw json.RawMessage) []string {
	var result struct {
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return nil
	}
	names := make([]string, 0, len(result.Models))
	for _, model := range result.Models {
		if model.ID != "" && !IsManagedModelSelector(model.ID) {
			names = append(names, model.ID)
		}
	}
	return names
}

func capabilityEvidenceResult(item AccountCapabilityItem) AccountCapabilityProbeResult {
	var result AccountCapabilityProbeResult
	_ = json.Unmarshal(item.Result, &result)
	if result.Status == "" {
		result.Status = item.Status
	}
	return result
}

func capabilitySuccessfulTextEvidence(item AccountCapabilityItem) bool {
	result := capabilityEvidenceResult(item)
	return capabilityIsBasicTextEvidence(item) && (item.Status == "succeeded" || item.Status == "") &&
		result.Status == "alive" && !result.AccountFailure
}

func capabilityKnownUnsentCancellation(item AccountCapabilityItem) bool {
	if item.Status != "canceled" || item.DispatchedAt != nil || item.RequestCount != 0 || item.RequestCountUnknown {
		return false
	}
	var result struct {
		RequestCount        int  `json:"request_count"`
		RequestCountUnknown bool `json:"request_count_unknown"`
	}
	_ = json.Unmarshal(item.Result, &result)
	return result.RequestCount == 0 && !result.RequestCountUnknown
}

// capabilityEvidenceSupersededBy mirrors the durable ledger predicate for
// in-memory snapshots. An account-level failure is definitive; a model or
// protocol failure affects only the exact basic-text line that was tested.
// Catalog successes cannot revive text evidence predating a credential failure.
func capabilityEvidenceSupersededBy(success AccountCapabilityItem, observations []AccountCapabilityItem) bool {
	if success.PublicationSuperseded {
		return true
	}
	for _, item := range observations {
		if item.AccountID != success.AccountID || item.FolderID != success.FolderID ||
			item.ConfigFingerprint != success.ConfigFingerprint || !capabilityItemNewer(item, success) {
			continue
		}
		if item.Kind != AccountCapabilityKindDiscover && !capabilityIsBasicTextEvidence(item) {
			continue
		}
		if item.Status != "failed" && item.Status != "" {
			continue
		}
		result := capabilityEvidenceResult(item)
		if result.AccountFailure && result.Status == "failed" {
			return true
		}
		if !capabilityIsBasicTextEvidence(item) || item.UpstreamModel != success.UpstreamModel || item.Protocol != success.Protocol ||
			(result.Status != "failed" && result.Status != "unsupported") {
			continue
		}
		switch result.Classification {
		case "model_unavailable", "protocol_unsupported":
			return true
		}
	}
	return false
}

func capabilityApplyCandidateEvidence(row *AccountCapabilityCandidate, account *Account, observations, accountObservations []AccountCapabilityItem) {
	var attempt, currentSuccess, historicalSuccess *AccountCapabilityItem
	for i := range observations {
		item := &observations[i]
		if capabilityKnownUnsentCancellation(*item) {
			// Known-unsent cancellations do not consume a probe. Interrupted
			// requests are stored as indeterminate and remain non-repeatable.
			continue
		}
		current := capabilityEvidenceCurrent(*item, account, row.ConfigFingerprint)
		row.AlreadyAttempted = row.AlreadyAttempted || current
		if attempt == nil || capabilityItemNewer(*item, *attempt) {
			attempt = item
		}
		if !capabilitySuccessfulTextEvidence(*item) {
			continue
		}
		if historicalSuccess == nil || capabilityItemNewer(*item, *historicalSuccess) {
			historicalSuccess = item
		}
		if current && (currentSuccess == nil || capabilityItemNewer(*item, *currentSuccess)) {
			currentSuccess = item
		}
	}
	if attempt != nil {
		result := capabilityEvidenceResult(*attempt)
		row.LatestProbeItemID = &attempt.ID
		row.CheckedAt = attempt.FinishedAt
		row.ProbeStatus = result.Status
		row.Stale = !capabilityEvidenceCurrent(*attempt, account, row.ConfigFingerprint)
		row.LatestAttempt = &AccountCapabilityAttempt{
			ItemID: attempt.ID, Status: result.Status, Classification: result.Classification,
			CheckedAt: attempt.FinishedAt, Stale: row.Stale, AccountFailure: result.AccountFailure,
		}
		if row.Stale {
			row.ProbeStatus = "stale"
		}
	}
	success := currentSuccess
	if success == nil {
		success = historicalSuccess
	}
	if success != nil {
		row.LastSuccessItemID, row.LastSuccessAt = &success.ID, success.FinishedAt
		if success == currentSuccess {
			superseded := capabilityEvidenceSupersededBy(*success, accountObservations)
			row.LastSuccessReusable = success.FinishedAt != nil && !superseded
			if superseded {
				row.NotPublishableReasons = append(row.NotPublishableReasons, "evidence_superseded")
			}
			if success.FinishedAt == nil {
				row.NotPublishableReasons = append(row.NotPublishableReasons, "evidence_incomplete")
			}
		}
		if success.FinishedAt != nil && success.FinishedAt.Before(time.Now().Add(-24*time.Hour)) {
			// Age describes evidence freshness, never a reason to run a paid
			// request again or silently withdraw an already published model.
			row.Warnings = append(row.Warnings, "historical_success")
		}
	}
	if !row.LastSuccessReusable && (row.Stale || (success != nil && currentSuccess == nil)) {
		row.NotPublishableReasons = append(row.NotPublishableReasons, "configuration_changed")
	}
	if row.LatestAttempt != nil && !row.LatestAttempt.Stale && row.LatestAttempt.Status != "alive" && row.LastSuccessReusable {
		row.Warnings = append(row.Warnings, "recent_failure_historical_success_retained")
	}
}

func capabilityHasCompatibleHistoricalSuccess(account *Account, fingerprint, upstream string, observations []AccountCapabilityItem) bool {
	for _, item := range observations {
		if item.UpstreamModel == upstream && capabilityEvidenceCurrent(item, account, fingerprint) &&
			capabilitySuccessfulTextEvidence(item) && len(CapabilityIngressEndpoints(account, item.Protocol)) > 0 {
			// This suppresses new protocol-expansion calls even if the previous
			// success was later superseded. Publication eligibility is separate.
			return true
		}
	}
	return false
}

func capabilityTargetProbeHistory(account *Account, fingerprint, upstream string, observations []AccountCapabilityItem) (bool, int) {
	pending := false
	protocols := map[string]bool{}
	for _, item := range observations {
		if item.UpstreamModel != upstream || !capabilityEvidenceCurrent(item, account, fingerprint) || !capabilityIsBasicTextEvidence(item) || capabilityKnownUnsentCancellation(item) {
			continue
		}
		if item.Status == "pending" || item.Status == "running" {
			pending = true
		}
		var result struct {
			RequestCount        int  `json:"request_count"`
			RequestCountUnknown bool `json:"request_count_unknown"`
		}
		_ = json.Unmarshal(item.Result, &result)
		if item.RequestCount > 0 || item.RequestCountUnknown || item.DispatchedAt != nil || item.Status == "failed" || item.Status == "indeterminate" || result.RequestCount > 0 || result.RequestCountUnknown {
			protocols[item.Protocol] = true
		}
	}
	return pending, len(protocols)
}

func capabilityHasCurrentAccountFailure(account *Account, fingerprint string, observations []AccountCapabilityItem) bool {
	for _, item := range observations {
		if !capabilityEvidenceCurrent(item, account, fingerprint) || item.PublicationSuperseded ||
			(item.Kind != AccountCapabilityKindDiscover && !capabilityIsBasicTextEvidence(item)) ||
			(item.Status != "failed" && item.Status != "") || !capabilityEvidenceResult(item).AccountFailure {
			continue
		}
		retired := false
		for _, newer := range observations {
			if !capabilityEvidenceCurrent(newer, account, fingerprint) || !capabilityItemNewer(newer, item) {
				continue
			}
			if capabilitySuccessfulTextEvidence(newer) {
				retired = true
				break
			}
			if newer.Kind != AccountCapabilityKindDiscover || (newer.Status != "succeeded" && newer.Status != "") {
				continue
			}
			var result struct {
				Source string `json:"source"`
				Status string `json:"status"`
			}
			_ = json.Unmarshal(newer.Result, &result)
			if result.Source == "upstream" && (result.Status == "discovered" || result.Status == "empty" || result.Status == "partial") {
				retired = true
				break
			}
		}
		if !retired {
			return true
		}
	}
	return false
}
