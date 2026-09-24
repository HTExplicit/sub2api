package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func accountCreateRequestJSON(t *testing.T, explicit bool) []byte {
	t.Helper()
	provider := extensionv1.ProviderCreateRequestV1{ContributionID: "cindy-create", ExpectedPackageSHA256: strings.Repeat("a", 64),
		ExpectedDefinitionSHA256: strings.Repeat("b", 64), ExpectedRuntimeGeneration: 3, Values: map[string]string{"device_id": ""},
		InheritDefaults: []string{"concurrency", "priority", "rate_multiplier", "load_factor", "responses_mode"}}
	fields := map[string]any{"name": "synthetic", "platform": "cindy", "type": "apikey", "credentials": cindyJobCredentials(), "provider_create": provider,
		"ExplicitCreateFields": map[string]bool{"priority": false, "load_factor": false}}
	if explicit {
		fields["concurrency"], fields["priority"], fields["rate_multiplier"], fields["load_factor"] = 0, 0, 0, nil
		fields["extra"] = map[string]any{"openai_responses_mode": "auto"}
	}
	raw, err := json.Marshal(fields)
	require.NoError(t, err)
	return raw
}

func TestAccountCreateTaggedBatchIntentSurvivesEncryptedPayload(t *testing.T) {
	var explicit, inherited CreateAccountRequest
	require.NoError(t, json.Unmarshal(accountCreateRequestJSON(t, true), &explicit))
	require.NoError(t, json.Unmarshal(accountCreateRequestJSON(t, false), &inherited))
	require.True(t, explicit.ExplicitCreateFields["priority"], "client-supplied presence maps are ignored")
	require.True(t, explicit.ExplicitCreateFields["load_factor"])
	raw, err := json.Marshal(batchCreateJobPayload{Accounts: []CreateAccountRequest{explicit, inherited}})
	require.NoError(t, err)
	repo := &accountJobSubmitRepository{}
	cipher := &codexImportJobOpaqueCipher{}
	jobs := service.NewAccountJobService(repo, cipher)
	_, _, err = jobs.Submit(context.Background(), accountJobTestActorID, service.AccountJobKindBatchCreate, "provider-create-batch", raw, nil, ordinalAccountJobSeeds(2))
	require.NoError(t, err)
	require.Len(t, repo.created, 1)
	params := repo.created[0]
	require.NotContains(t, params.PayloadCipher, "test-key")
	require.NotContains(t, string(params.Metadata), "test-key")
	plain, err := cipher.Decrypt(params.PayloadCipher)
	require.NoError(t, err)
	var decoded batchCreateJobPayload
	require.NoError(t, json.Unmarshal([]byte(plain), &decoded))
	require.Len(t, decoded.Accounts, 2)
	for _, target := range []string{"concurrency", "priority", "rate_multiplier", "load_factor"} {
		require.True(t, decoded.Accounts[0].ExplicitCreateFields[target])
		require.False(t, decoded.Accounts[1].ExplicitCreateFields[target], "an inherited omitted number must not turn into serialized explicit zero/null")
	}
	require.Equal(t, "auto", decoded.Accounts[0].Extra[service.CindyResponsesModeExtraKey])
	require.Equal(t, explicit.ProviderCreate, decoded.Accounts[0].ProviderCreate)
	require.Equal(t, inherited.ProviderCreate, decoded.Accounts[1].ProviderCreate)
	admin := newStubAdminService()
	handler := NewAccountHandler(admin, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler.cindyJobMutations = &recordingCindyJobMutationRunner{}
	for index := range decoded.Accounts {
		result := handler.executeAccountJobItem(context.Background(), service.AccountJobKindBatchCreate, []byte(plain), service.AccountJobItem{ID: int64(index + 1), Ordinal: index + 1})
		require.Equal(t, service.AccountJobItemStatusSucceeded, result.Status)
	}
	require.Len(t, admin.createdAccounts, 2)
	require.Equal(t, explicit.ProviderCreate, admin.createdAccounts[0].ProviderCreate)
	require.True(t, admin.createdAccounts[0].ExplicitCreateFields["load_factor"])
	require.Nil(t, admin.createdAccounts[0].LoadFactor)
	require.Equal(t, "auto", admin.createdAccounts[0].Extra[service.CindyResponsesModeExtraKey])
	require.False(t, admin.createdAccounts[1].ExplicitCreateFields["concurrency"])
	require.WithinDuration(t, time.Now().UTC().Add(24*time.Hour), params.PayloadExpires, time.Second)
}

func TestAccountCreateNativeNumericHashAndStrictProviderBlock(t *testing.T) {
	var native CreateAccountRequest
	require.NoError(t, json.Unmarshal([]byte(`{"name":"native","platform":"openai","type":"apikey","concurrency":0,"priority":0,"load_factor":null,"credentials":{"api_key":"native-key"}}`), &native))
	require.Nil(t, native.ExplicitCreateFields)
	type originalShape CreateAccountRequest
	previousJSON, err := json.Marshal(originalShape(native))
	require.NoError(t, err)
	currentJSON, err := json.Marshal(native)
	require.NoError(t, err)
	require.Equal(t, previousJSON, currentJSON, "the no-tag request retains its old field ordering and zero/null representation")
	digest := sha256.Sum256(previousJSON)
	repo := &accountJobSubmitRepository{}
	jobs := service.NewAccountJobService(repo, accountJobTestEncryptor{})
	_, _, err = jobs.Submit(context.Background(), accountJobTestActorID, service.AccountJobKindBatchCreate, "native-hash", currentJSON, nil, ordinalAccountJobSeeds(1))
	require.NoError(t, err)
	require.Equal(t, hex.EncodeToString(digest[:]), repo.created[0].RequestHash)
	valid := accountCreateRequestJSON(t, true)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(valid, &fields))
	provider, ok := fields["provider_create"].(map[string]any)
	require.True(t, ok)
	provider["credentials"] = map[string]any{"api_key": "private-must-not-be-echoed"}
	invalid, err := json.Marshal(fields)
	require.NoError(t, err)
	err = json.Unmarshal(invalid, &native)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private-must-not-be-echoed")
	delete(provider, "credentials")
	provider["values"] = map[string]any{"device_id": nil}
	invalid, err = json.Marshal(fields)
	require.NoError(t, err)
	require.Error(t, json.Unmarshal(invalid, &native), "values are strings, not null or arbitrary objects")
}
