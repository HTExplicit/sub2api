package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/stretchr/testify/require"
)

type importPlanFixture struct {
	payloadSizes []int
	requests     []extensionv1.AccountImportPlanningRequest
	corrupt      bool
	mutate       func(extensionv1.AccountImportPlanningRequest, []extensionv1.AccountImportItemPlan)
}

func (f *importPlanFixture) invoke(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
	var request extensionv1.AccountImportPlanningRequest
	if in.Operation == "import.plan" {
		f.payloadSizes = append(f.payloadSizes, len(in.Payload))
		if err := json.Unmarshal(in.Payload, &request); err != nil {
			return extensionv1.Result{}, err
		}
		f.requests = append(f.requests, request)
		if f.corrupt {
			raw, _ := json.Marshal([]extensionv1.AccountImportItemPlan{{Index: 0, Action: "update", Code: extensionv1.AccountImportCodeUpdate, AccountID: 999}})
			return extensionv1.Result{Payload: raw}, nil
		}
	}
	result, err := accountTools().Invoke(ctx, in)
	if err != nil || f.mutate == nil || in.Operation != "import.plan" {
		return result, err
	}
	var plans []extensionv1.AccountImportItemPlan
	if err := json.Unmarshal(result.Payload, &plans); err != nil {
		return result, err
	}
	f.mutate(request, plans)
	result.Payload, err = json.Marshal(plans)
	return result, err
}

func installImportPlanFixture(t *testing.T, fixture *importPlanFixture) {
	t.Helper()
	replaceAccountTools(t, fixture.invoke)
}

func importPlanRequestWithOrdinaryItems(count int) extensionv1.AccountImportPlanningRequest {
	request := extensionv1.AccountImportPlanningRequest{TargetGroupID: 12, TargetCanonical: true, Items: make([]extensionv1.AccountImportItemFacts, count)}
	for index := range request.Items {
		request.Items[index].PayloadValid = true
	}
	return request
}

func TestAccountImportPlanKeepsCrossChunkConflictPriorityAndScope(t *testing.T) {
	fixture := &importPlanFixture{}
	installImportPlanFixture(t, fixture)
	input := extensionv1.AccountImportPlanningRequest{TargetGroupID: 12, TargetCanonical: true, Items: make([]extensionv1.AccountImportItemFacts, 1002)}
	for index := range input.Items {
		input.Items[index].PayloadValid = true
	}
	duplicate := extensionv1.AccountImportItemFacts{CindyCandidate: true, APIKeyValid: true, PayloadValid: true, DeviceValid: true, DeviceSourceValid: true, CredentialIdentity: "credential-digest", DeviceIdentity: "device-digest"}
	input.Items[0], input.Items[1001] = duplicate, duplicate
	// Match ambiguity is rejected after tracking, so it must still prevent a
	// second item in another chunk from using the same credential or device.
	input.Items[0].Matches = []int64{38, 39}
	input.Items[10] = extensionv1.AccountImportItemFacts{PayloadValid: true, Matches: []int64{37}}
	input.Items[20] = duplicate
	input.Items[20].CredentialIdentity, input.Items[20].DeviceIdentity = "unique-create", "unique-create-device"
	input.Items[21] = duplicate
	input.Items[21].CredentialIdentity, input.Items[21].DeviceIdentity = "unique-update", "unique-update-device"
	input.Items[21].Matches, input.Items[21].DeviceOwners = []int64{41}, []int64{41}
	plans, err := PlanAccountImport(context.Background(), input)
	require.NoError(t, err)
	require.Len(t, plans, len(input.Items))
	for _, index := range []int{0, 1001} {
		require.Equal(t, extensionv1.AccountImportCodeDeviceConflict, plans[index].Code)
		require.Equal(t, "reject", plans[index].Action)
		require.True(t, plans[index].TrackCredential)
		require.True(t, plans[index].TrackDevice)
	}
	require.EqualValues(t, 37, plans[10].AccountID)
	require.Equal(t, "create", plans[20].Action)
	require.Equal(t, "update", plans[21].Action)
	require.EqualValues(t, 41, plans[21].AccountID)
	require.Equal(t, []int64{12}, plans[20].GroupIDs)
	require.Equal(t, []int64{12}, plans[21].GroupIDs)
	require.Len(t, fixture.payloadSizes, 4)
	for _, size := range fixture.payloadSizes {
		require.Less(t, size, extensionv1.MaxPayloadBytes)
	}
	fixture.corrupt = true
	_, err = PlanAccountImport(context.Background(), extensionv1.AccountImportPlanningRequest{Items: []extensionv1.AccountImportItemFacts{{PayloadValid: true, Matches: []int64{37}}}})
	require.ErrorIs(t, err, ErrExtensionOperationUnavailable, "a plan cannot choose another existing account")
	replaceAccountTools(t, func(context.Context, extensionv1.Invocation) (extensionv1.Result, error) {
		return extensionv1.Result{}, errors.New("policy failed")
	})
	_, err = PlanAccountImport(context.Background(), input)
	require.Error(t, err, "a failed policy must not fall back to a host decision")
}

func TestAccountImportPlanRejectsForgedCrossChunkTrackingFlags(t *testing.T) {
	for _, phase := range []string{"prepare", "finalize"} {
		t.Run(phase, func(t *testing.T) {
			fixture := &importPlanFixture{mutate: func(request extensionv1.AccountImportPlanningRequest, plans []extensionv1.AccountImportItemPlan) {
				if request.Phase == phase {
					plans[0].TrackCredential, plans[0].TrackDevice = false, false
				}
			}}
			installImportPlanFixture(t, fixture)
			input := importPlanRequestWithOrdinaryItems(1002)
			duplicate := extensionv1.AccountImportItemFacts{CindyCandidate: true, APIKeyValid: true, PayloadValid: true, DeviceValid: true, DeviceSourceValid: true, CredentialIdentity: "credential-digest", DeviceIdentity: "device-digest"}
			input.Items[0], input.Items[1001] = duplicate, duplicate
			// Rejected match ambiguity still participates in identity safety;
			// checking flags only on create/update plans would miss this forgery.
			input.Items[0].Matches = []int64{37, 38}
			plans, err := PlanAccountImport(context.Background(), input)
			require.ErrorIs(t, err, ErrExtensionOperationUnavailable, "host must not trust forged tracking hints")
			require.Nil(t, plans)
			if phase == "prepare" {
				require.Len(t, fixture.requests, 1, "a forged prepare plan must fail before finalize")
			} else {
				require.Len(t, fixture.requests, 3, "valid prepare chunks must reach the forged finalize response")
			}
		})
	}
}

func TestAccountImportPlanDoesNotCountEarlyRejectedItemsAcrossChunks(t *testing.T) {
	fixture := &importPlanFixture{}
	installImportPlanFixture(t, fixture)
	cases := []struct {
		name   string
		code   string
		change func(*extensionv1.AccountImportItemFacts)
	}{
		{"api-key", extensionv1.AccountImportCodeCindyAPIKeyInvalid, func(f *extensionv1.AccountImportItemFacts) {
			f.APIKeyValid, f.PayloadValid, f.DeviceValid = false, false, false
		}},
		{"legacy-api-key", extensionv1.AccountImportCodeCindyAPIKeyInvalid, func(f *extensionv1.AccountImportItemFacts) { f.LegacyCindy, f.APIKeyValid = true, false }},
		{"payload", extensionv1.AccountImportCodePayloadInvalid, func(f *extensionv1.AccountImportItemFacts) { f.PayloadValid, f.DeviceValid = false, false }},
		{"device", extensionv1.AccountImportCodeDeviceInvalid, func(f *extensionv1.AccountImportItemFacts) { f.DeviceValid = false }},
		{"device-source", extensionv1.AccountImportCodeDeviceInvalid, func(f *extensionv1.AccountImportItemFacts) { f.DeviceSourceValid = false }},
	}
	input := importPlanRequestWithOrdinaryItems(1000 + len(cases))
	for index, tc := range cases {
		valid := extensionv1.AccountImportItemFacts{CindyCandidate: true, APIKeyValid: true, PayloadValid: true, DeviceValid: true, DeviceSourceValid: true, CredentialIdentity: tc.name + "-credential", DeviceIdentity: tc.name + "-device"}
		input.Items[index], input.Items[1000+index] = valid, valid
		tc.change(&input.Items[index])
	}
	plans, err := PlanAccountImport(context.Background(), input)
	require.NoError(t, err)
	require.Len(t, fixture.requests, 4)
	for index, tc := range cases {
		require.Equal(t, "reject", plans[index].Action, tc.name)
		require.Equal(t, tc.code, plans[index].Code, tc.name)
		require.False(t, plans[index].TrackCredential, tc.name)
		require.False(t, plans[index].TrackDevice, tc.name)
		require.Equal(t, "create", plans[1000+index].Action, "early reject must not poison its valid counterpart: %s", tc.name)
		require.Zero(t, fixture.requests[2].Items[index].CredentialCopies, tc.name)
		require.Zero(t, fixture.requests[2].Items[index].DeviceCopies, tc.name)
		require.Equal(t, 1, fixture.requests[3].Items[index].CredentialCopies, tc.name)
		require.Equal(t, 1, fixture.requests[3].Items[index].DeviceCopies, tc.name)
	}
}

func TestAccountImportPlanKeepsDeviceOwnerConflictPriorityAfterCredentialConflict(t *testing.T) {
	for _, count := range []int{2, 1001} {
		fixture := &importPlanFixture{}
		installImportPlanFixture(t, fixture)
		input := importPlanRequestWithOrdinaryItems(count)
		duplicate := extensionv1.AccountImportItemFacts{CindyCandidate: true, APIKeyValid: true, PayloadValid: true, DeviceValid: true, DeviceSourceValid: true, CredentialIdentity: "shared-credential", Matches: []int64{37}}
		input.Items[0], input.Items[count-1] = duplicate, duplicate
		input.Items[0].DeviceIdentity, input.Items[0].DeviceOwners = "owned-device", []int64{37}
		plans, err := PlanAccountImport(context.Background(), input)
		require.NoError(t, err, "device ownership is checked after credential rejection clears the update account: count=%d", count)
		require.Equal(t, "reject", plans[0].Action)
		require.Equal(t, extensionv1.AccountImportCodeDeviceConflict, plans[0].Code)
		require.Equal(t, "reject", plans[count-1].Action)
		require.Equal(t, extensionv1.AccountImportCodeCredentialConflict, plans[count-1].Code)
		require.Zero(t, plans[0].AccountID)
		require.Zero(t, plans[count-1].AccountID)
	}
}

func TestAccountImportPlanRejectsForgedFinalizedDuplicateExecution(t *testing.T) {
	for _, count := range []int{2, 1001} {
		fixture := &importPlanFixture{mutate: func(request extensionv1.AccountImportPlanningRequest, plans []extensionv1.AccountImportItemPlan) {
			if request.Phase != "prepare" {
				for index := range plans {
					if plans[index].CanonicalCindy {
						plans[index].Action, plans[index].Code = "create", extensionv1.AccountImportCodeCreate
					}
				}
			}
		}}
		installImportPlanFixture(t, fixture)
		input := importPlanRequestWithOrdinaryItems(count)
		duplicate := extensionv1.AccountImportItemFacts{CindyCandidate: true, APIKeyValid: true, PayloadValid: true, DeviceValid: true, DeviceSourceValid: true, CredentialIdentity: "shared-credential", DeviceIdentity: "shared-device"}
		input.Items[0], input.Items[count-1] = duplicate, duplicate
		plans, err := PlanAccountImport(context.Background(), input)
		require.ErrorIs(t, err, ErrExtensionOperationUnavailable, "valid tracking flags cannot authorize duplicate execution: count=%d", count)
		require.Nil(t, plans)
		if count > 1000 {
			require.Len(t, fixture.requests, 4)
			require.Equal(t, 2, fixture.requests[2].Items[0].CredentialCopies)
			require.Equal(t, 2, fixture.requests[3].Items[0].DeviceCopies)
		}
	}
}

func TestAccountImportPlanRejectsForgedExecutionOutsideIdentityScope(t *testing.T) {
	cases := []struct {
		name   string
		code   string
		change func(*extensionv1.AccountImportPlanningRequest)
	}{
		{"early-reject-tracking", extensionv1.AccountImportCodePayloadInvalid, func(in *extensionv1.AccountImportPlanningRequest) { in.Items[0].PayloadValid = false }},
		{"invalid-target", extensionv1.AccountImportCodeCindyTargetInvalid, func(in *extensionv1.AccountImportPlanningRequest) { in.TargetHasMembers = true }},
		{"invalid-legacy-api-key", extensionv1.AccountImportCodeCindyAPIKeyInvalid, func(in *extensionv1.AccountImportPlanningRequest) {
			in.Items[0].LegacyCindy, in.Items[0].APIKeyValid = true, false
		}},
		{"device-owner", extensionv1.AccountImportCodeDeviceConflict, func(in *extensionv1.AccountImportPlanningRequest) { in.Items[0].DeviceOwners = []int64{99} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := &importPlanFixture{}
			installImportPlanFixture(t, fixture)
			input := importPlanRequestWithOrdinaryItems(1)
			input.Items[0] = extensionv1.AccountImportItemFacts{CindyCandidate: true, APIKeyValid: true, PayloadValid: true, DeviceValid: true, DeviceSourceValid: true, CredentialIdentity: "credential", DeviceIdentity: "device"}
			tc.change(&input)
			plans, err := PlanAccountImport(context.Background(), input)
			require.NoError(t, err, "valid plugin rejection must remain item-local")
			require.Equal(t, "reject", plans[0].Action)
			require.Equal(t, tc.code, plans[0].Code)

			fixture.mutate = func(_ extensionv1.AccountImportPlanningRequest, plans []extensionv1.AccountImportItemPlan) {
				if tc.name == "early-reject-tracking" {
					plans[0].TrackCredential, plans[0].TrackDevice = true, true
					return
				}
				plans[0].Action, plans[0].Code, plans[0].AccountID = "create", extensionv1.AccountImportCodeCreate, 0
				if plans[0].CanonicalCindy {
					plans[0].GroupIDs = []int64{12}
				}
			}
			plans, err = PlanAccountImport(context.Background(), input)
			require.ErrorIs(t, err, ErrExtensionOperationUnavailable)
			require.Nil(t, plans, "forged policy response must not yield any executable plan")
		})
	}
}
