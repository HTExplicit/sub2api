package admin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func strictCindyImportGroup(id int64) service.Group {
	return service.Group{
		ID: id, Name: "Cindy", Platform: service.PlatformCindy,
		WirePlatform: service.WirePlatformOpenAI, ProviderProfile: service.ProviderProfileCindyLaxaV1,
		Status: service.StatusActive, Hydrated: true, StrictCindyKnown: true, StrictCindy: true,
	}
}

func cindyImportAccount(name, platform, key, deviceID string) DataAccount {
	extra := map[string]any{}
	if deviceID != "" {
		extra[service.CindyDeviceIDExtraKey] = deviceID
		extra[service.CindyDeviceIDSourceExtraKey] = "input-preserved"
	}
	return DataAccount{
		Name: name, Platform: platform, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://api.laxarouter.ai/", "api_key": key},
		Extra:       extra, Concurrency: 1, Priority: 1,
	}
}

func cindyImportRequest(groupID *int64, accounts ...DataAccount) DataImportRequest {
	return DataImportRequest{
		Data:          DataPayload{Type: dataType, Version: dataVersion, Proxies: []DataProxy{}, Accounts: accounts},
		TargetGroupID: groupID,
	}
}

func TestDataImportDecisionPromotesOnlyExactLegacyCindy(t *testing.T) {
	groupID := int64(88)
	svc := newDataV2AdminService()
	svc.groups = []service.Group{strictCindyImportGroup(groupID)}
	handler := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	exact := cindyImportAccount("exact", service.PlatformOpenAI, "key-exact", "")
	near := cindyImportAccount("near", service.PlatformOpenAI, "key-near", "")
	near.Credentials["base_url"] = "https://api.laxarouter.ai/v1"
	preview, decisions, err := handler.previewDataImport(context.Background(), cindyImportRequest(&groupID, exact, near))

	require.NoError(t, err)
	require.Equal(t, []string{dataImportActionCreate, dataImportActionCreate}, []string{preview.Items[0].Action, preview.Items[1].Action})
	require.Equal(t, service.PlatformCindy, decisions[0].Account.Platform)
	require.Equal(t, service.PlatformOpenAI, decisions[1].Account.Platform)
	require.Equal(t, []int64{groupID}, decisions[0].GroupIDs)
}

func TestDataImportDecisionRequiresOneExplicitStrictCindyTargetGroup(t *testing.T) {
	strictID := int64(88)
	ordinaryID := int64(89)
	svc := newDataV2AdminService()
	svc.groups = []service.Group{
		strictCindyImportGroup(strictID),
		{ID: ordinaryID, Name: "ordinary", Platform: service.PlatformOpenAI, Status: service.StatusActive},
	}
	handler := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	account := cindyImportAccount("cindy", service.PlatformCindy, "key", "")
	account.Groups = []string{"cindy"}

	missing, _, err := handler.previewDataImport(context.Background(), cindyImportRequest(nil, account))
	require.NoError(t, err)
	require.Equal(t, dataImportActionReject, missing.Items[0].Action)
	require.Equal(t, dataImportCodeCindyTargetRequired, missing.Items[0].Code)

	invalid, _, err := handler.previewDataImport(context.Background(), cindyImportRequest(&ordinaryID, account))
	require.NoError(t, err)
	require.Equal(t, dataImportActionReject, invalid.Items[0].Action)
	require.Equal(t, dataImportCodeCindyTargetInvalid, invalid.Items[0].Code)

	valid, decisions, err := handler.previewDataImport(context.Background(), cindyImportRequest(&strictID, account))
	require.NoError(t, err)
	require.Equal(t, dataImportActionCreate, valid.Items[0].Action)
	require.Equal(t, []int64{strictID}, decisions[0].GroupIDs)
	require.Empty(t, decisions[0].Account.Groups, "legacy group names are never import authority")
}

func TestDataImportDecisionPropagatesTargetGroupLookupFailure(t *testing.T) {
	groupID := int64(88)
	svc := newDataV2AdminService()
	svc.getGroupErr = errors.New("synthetic group store failure")
	handler := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	_, _, err := handler.previewDataImport(context.Background(), cindyImportRequest(
		&groupID,
		cindyImportAccount("cindy", service.PlatformCindy, "key", ""),
	))

	require.EqualError(t, err, "synthetic group store failure")
}

func TestDataImportPreviewAndCommitShareDecisionEngineAtExecutionTime(t *testing.T) {
	groupID := int64(88)
	svc := newDataV2AdminService()
	svc.groups = []service.Group{strictCindyImportGroup(groupID)}
	runner := &recordingCindyJobMutationRunner{}
	handler := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler.cindyJobMutations = runner
	req := cindyImportRequest(&groupID, cindyImportAccount("incoming", service.PlatformCindy, "stale-key", ""))

	preview, _, err := handler.previewDataImport(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, dataImportActionCreate, preview.Items[0].Action)
	require.Empty(t, svc.createdAccounts, "preview must be read-only")

	svc.accounts = append(svc.accounts, service.Account{
		ID: 44, Name: "now-existing", Platform: service.PlatformCindy,
		WirePlatform: service.WirePlatformOpenAI, ProviderProfile: service.ProviderProfileCindyLaxaV1,
		Type: service.AccountTypeAPIKey, Credentials: cindyJobCredentials(),
		GroupIDs: []int64{groupID}, Status: service.StatusActive, Schedulable: true,
	})
	svc.accounts[len(svc.accounts)-1].Credentials["api_key"] = "stale-key"

	result := executeAccountJobTestItem(t, handler, service.AccountJobKindImportData, req, service.AccountJobItem{ID: 91, Ordinal: 1})
	require.Equal(t, service.AccountJobItemStatusSucceeded, result.Status)
	var metadata map[string]any
	require.NoError(t, json.Unmarshal(result.Metadata, &metadata))
	require.Equal(t, dataImportActionUpdate, metadata["action"])
	require.Equal(t, []int64{44}, runner.accountIDs)
}

func TestDataImportDecisionRejectsCredentialAndDeviceConflictsDeterministically(t *testing.T) {
	groupID := int64(88)
	deviceID := strings.Repeat("a", 64)
	svc := newDataV2AdminService()
	svc.groups = []service.Group{strictCindyImportGroup(groupID)}
	handler := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	duplicateCredential, _, err := handler.previewDataImport(context.Background(), cindyImportRequest(&groupID,
		cindyImportAccount("first", service.PlatformCindy, "same-key", strings.Repeat("b", 64)),
		cindyImportAccount("second", service.PlatformCindy, "same-key", strings.Repeat("c", 64)),
	))
	require.NoError(t, err)
	require.Equal(t, []string{dataImportCodeCindyCredentialConflict, dataImportCodeCindyCredentialConflict},
		[]string{duplicateCredential.Items[0].Code, duplicateCredential.Items[1].Code})

	duplicateDevice, _, err := handler.previewDataImport(context.Background(), cindyImportRequest(&groupID,
		cindyImportAccount("first", service.PlatformCindy, "key-one", deviceID),
		cindyImportAccount("second", service.PlatformCindy, "key-two", deviceID),
	))
	require.NoError(t, err)
	require.Equal(t, []string{dataImportCodeCindyDeviceConflict, dataImportCodeCindyDeviceConflict},
		[]string{duplicateDevice.Items[0].Code, duplicateDevice.Items[1].Code})
}

func TestDataImportPreviewRejectsWithStableRedactedBusinessError(t *testing.T) {
	groupID := int64(88)
	svc := newDataV2AdminService()
	svc.groups = []service.Group{strictCindyImportGroup(groupID)}
	handler := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	secret := "sk-private-preview-secret"
	account := cindyImportAccount("invalid", service.PlatformCindy, secret, "not-a-device")

	preview, _, err := handler.previewDataImport(context.Background(), cindyImportRequest(&groupID, account))
	require.NoError(t, err)
	require.Equal(t, dataImportActionReject, preview.Items[0].Action)
	require.Equal(t, dataImportCodeCindyDeviceInvalid, preview.Items[0].Code)
	encoded, err := json.Marshal(preview)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), secret)
	require.NotContains(t, string(encoded), "not-a-device")

	sourceSecret := "private-device-source"
	account = cindyImportAccount("invalid-source", service.PlatformCindy, "source-key", strings.Repeat("a", 64))
	account.Extra[service.CindyDeviceIDSourceExtraKey] = sourceSecret
	preview, _, err = handler.previewDataImport(context.Background(), cindyImportRequest(&groupID, account))
	require.NoError(t, err)
	require.Equal(t, dataImportActionReject, preview.Items[0].Action)
	require.Equal(t, dataImportCodeCindyDeviceInvalid, preview.Items[0].Code)
	encoded, err = json.Marshal(preview)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), sourceSecret)
}

func TestAccountJobImportPreservesSafeDecisionError(t *testing.T) {
	groupID := int64(88)
	svc := newDataV2AdminService()
	svc.groups = []service.Group{strictCindyImportGroup(groupID)}
	handler := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	req := cindyImportRequest(&groupID,
		cindyImportAccount("first", service.PlatformCindy, "same-key", strings.Repeat("b", 64)),
		cindyImportAccount("second", service.PlatformCindy, "same-key", strings.Repeat("c", 64)),
	)

	result := executeAccountJobTestItem(t, handler, service.AccountJobKindImportData, req, service.AccountJobItem{ID: 92, Ordinal: 1})
	require.Equal(t, service.AccountJobItemStatusFailed, result.Status)
	require.Equal(t, dataImportCodeCindyCredentialConflict, result.ErrorCode)
	require.Equal(t, "credential is duplicated in the submitted import", result.ErrorMessage)
}
