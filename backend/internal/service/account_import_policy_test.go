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
	corrupt      bool
}

func (f *importPlanFixture) invoke(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
	if in.Operation == "import.plan" {
		f.payloadSizes = append(f.payloadSizes, len(in.Payload))
		if f.corrupt {
			raw, _ := json.Marshal([]extensionv1.AccountImportItemPlan{{Index: 0, Action: "update", Code: extensionv1.AccountImportCodeUpdate, AccountID: 999}})
			return extensionv1.Result{Payload: raw}, nil
		}
	}
	return accountTools().Invoke(ctx, in)
}

func TestAccountImportPlanChunksLargeImportsAndRejectsForgedPlans(t *testing.T) {
	fixture := &importPlanFixture{}
	replaceAccountTools(t, fixture.invoke)
	input := extensionv1.AccountImportPlanningRequest{Items: make([]extensionv1.AccountImportItemFacts, 1002)}
	for index := range input.Items {
		input.Items[index].PayloadValid = true
	}
	input.Items[0].Matches = []int64{38, 39}
	input.Items[10].Matches = []int64{37}
	input.Items[1001].PayloadValid = false
	plans, err := PlanAccountImport(context.Background(), input)
	require.NoError(t, err)
	require.Len(t, plans, len(input.Items))
	require.Equal(t, "reject", plans[0].Action)
	require.Equal(t, extensionv1.AccountImportCodeIdentityConflict, plans[0].Code)
	require.Equal(t, "update", plans[10].Action)
	require.EqualValues(t, 37, plans[10].AccountID)
	require.Equal(t, "create", plans[20].Action)
	require.Equal(t, 1001, plans[1001].Index)
	require.Equal(t, extensionv1.AccountImportCodePayloadInvalid, plans[1001].Code)
	require.Len(t, fixture.payloadSizes, 4, "two prepared chunks are finalized separately")
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
