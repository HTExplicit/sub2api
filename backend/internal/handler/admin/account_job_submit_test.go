package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const accountJobTestActorID int64 = 77

type accountJobSubmitRepository struct {
	service.AccountJobRepository
	created []service.CreateAccountJobParams
}

func (r *accountJobSubmitRepository) FindIdempotent(context.Context, int64, string, string) (*service.AccountJob, error) {
	return nil, service.ErrAccountJobNotFound
}

func (r *accountJobSubmitRepository) Create(_ context.Context, params service.CreateAccountJobParams) (*service.AccountJob, bool, error) {
	params.Items = append([]service.AccountJobItemSeed(nil), params.Items...)
	r.created = append(r.created, params)
	return &service.AccountJob{
		ID:             int64(len(r.created)),
		CreatedBy:      params.CreatedBy,
		Kind:           params.Kind,
		Status:         service.AccountJobStatusPending,
		Metadata:       params.Metadata,
		TargetCount:    len(params.Items),
		Attempt:        params.Attempt,
		IdempotencyKey: params.IdempotencyKey,
		RequestHash:    params.RequestHash,
	}, false, nil
}

type accountJobTestEncryptor struct{}

func (accountJobTestEncryptor) Encrypt(plaintext string) (string, error)  { return plaintext, nil }
func (accountJobTestEncryptor) Decrypt(ciphertext string) (string, error) { return ciphertext, nil }

func attachAccountJobSubmitter(router *gin.Engine, handler *AccountHandler) *accountJobSubmitRepository {
	repo := &accountJobSubmitRepository{}
	handler.SetAccountJobService(service.NewAccountJobService(repo, accountJobTestEncryptor{}))
	router.Use(func(c *gin.Context) {
		c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: accountJobTestActorID})
		c.Next()
	})
	return repo
}

func requireSubmittedAccountJob(t *testing.T, repo *accountJobSubmitRepository, kind string, payload any) service.CreateAccountJobParams {
	t.Helper()
	require.Len(t, repo.created, 1)
	params := repo.created[0]
	require.Equal(t, accountJobTestActorID, params.CreatedBy)
	require.Equal(t, kind, params.Kind)
	require.NotEmpty(t, params.IdempotencyKey)
	require.NotEmpty(t, params.RequestHash)
	require.Equal(t, 1, params.Attempt)
	require.NoError(t, json.Unmarshal([]byte(params.PayloadCipher), payload))
	return params
}

func executeSubmittedAccountJobItems(handler *AccountHandler, params service.CreateAccountJobParams) []service.AccountJobExecutionResult {
	results := make([]service.AccountJobExecutionResult, 0, len(params.Items))
	for index, seed := range params.Items {
		results = append(results, handler.executeAccountJobItem(context.Background(), params.Kind, json.RawMessage(params.PayloadCipher), service.AccountJobItem{
			ID:              int64(index + 1),
			Ordinal:         seed.Ordinal,
			Action:          seed.Action,
			TargetAccountID: seed.TargetAccountID,
			Metadata:        seed.Metadata,
		}))
	}
	return results
}

func setAccountJobTestIdempotencyKey(request *http.Request) {
	request.Header.Set("Idempotency-Key", "test-idempotency-key")
}
