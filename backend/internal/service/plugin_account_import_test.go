package service

import (
	"context"
	"encoding/json"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

type importPlanFixture struct {
	promptPolicyFixture
	payloadSizes []int
	corrupt      bool
}

func (f *importPlanFixture) InvokeOperation(ctx context.Context, platform, kind string, in extensionv1.Invocation) (extensionv1.Result, error) {
	if in.Operation == "import.plan" {
		f.payloadSizes = append(f.payloadSizes, len(in.Payload))
		if f.corrupt {
			raw, _ := json.Marshal([]extensionv1.AccountImportItemPlan{{Index: 0, Action: "update", Code: extensionv1.AccountImportCodeUpdate, AccountID: 999}})
			return extensionv1.Result{Payload: raw}, nil
		}
	}
	return f.promptPolicyFixture.InvokeOperation(ctx, platform, kind, in)
}

func TestAccountImportPlanKeepsCrossChunkConflictPriorityAndScope(t *testing.T) {
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	fixture := &importPlanFixture{}
	processExtensionOperations.Store(&extensionOperationProvider{invoker: fixture})
	input := extensionv1.AccountImportPlanningRequest{TargetGroupID: 12, TargetCanonical: true, Items: make([]extensionv1.AccountImportItemFacts, 1002)}
	for index := range input.Items {
		input.Items[index].PayloadValid = true
	}
	duplicate := extensionv1.AccountImportItemFacts{CindyCandidate: true, APIKeyValid: true, PayloadValid: true, DeviceValid: true, DeviceSourceValid: true, CredentialIdentity: "credential-digest", DeviceIdentity: "device-digest"}
	input.Items[0], input.Items[1001] = duplicate, duplicate
	input.Items[10] = extensionv1.AccountImportItemFacts{PayloadValid: true, Matches: []int64{37}}
	plans, err := PlanAccountImport(context.Background(), input)
	require.NoError(t, err)
	require.Len(t, plans, len(input.Items))
	for _, index := range []int{0, 1001} {
		require.Equal(t, extensionv1.AccountImportCodeDeviceConflict, plans[index].Code)
		require.Equal(t, "reject", plans[index].Action)
	}
	require.EqualValues(t, 37, plans[10].AccountID)
	require.Len(t, fixture.payloadSizes, 4)
	for _, size := range fixture.payloadSizes {
		require.Less(t, size, extensionv1.MaxPayloadBytes)
	}
	fixture.corrupt = true
	_, err = PlanAccountImport(context.Background(), extensionv1.AccountImportPlanningRequest{Items: []extensionv1.AccountImportItemFacts{{PayloadValid: true, Matches: []int64{37}}}})
	require.ErrorIs(t, err, ErrExtensionOperationUnavailable, "plugin cannot choose another existing account")
	processExtensionOperations.Store(nil)
	_, err = PlanAccountImport(context.Background(), input)
	require.Error(t, err, "disabled policy must not restore the host decision engine")
}
