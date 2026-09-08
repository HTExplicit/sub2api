package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type capabilityHandlerAccountsStub struct {
	service.AccountRepository
	reads int
}

func (s *capabilityHandlerAccountsStub) GetByIDs(context.Context, []int64) ([]*service.Account, error) {
	s.reads++
	folder := int64(7)
	return []*service.Account{{ID: 31, Name: "fixture", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, ManagementFolderID: &folder, Credentials: map[string]any{"api_key": "offline-secret-fixture", "base_url": "https://example.invalid"}}}, nil
}

type capabilityHandlerRepoStub struct {
	service.AccountCapabilityRepository
	run   *service.AccountCapabilityRun
	items []service.AccountCapabilityItem
}

func (s *capabilityHandlerRepoStub) FindIdempotent(context.Context, int64, string) (*service.AccountCapabilityRun, error) {
	if s.run == nil {
		return nil, service.ErrAccountCapabilityNotFound
	}
	return s.run, nil
}
func (s *capabilityHandlerRepoStub) Create(_ context.Context, run *service.AccountCapabilityRun, items []service.AccountCapabilityItem) (*service.AccountCapabilityRun, bool, error) {
	run.ID = 10
	s.run = run
	s.items = items
	return run, false, nil
}

func capabilityHandlerRouter(h *AccountCapabilityHandler, auth bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	if auth {
		router.Use(func(c *gin.Context) {
			c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 77})
			c.Next()
		})
	}
	router.POST("/runs", h.CreateRun)
	router.GET("/inventory", h.Inventory)
	return router
}

func TestAccountCapabilityJobsHandlerCreatesFrozenSafeRunAndReplays(t *testing.T) {
	repo := &capabilityHandlerRepoStub{}
	accounts := &capabilityHandlerAccountsStub{}
	router := capabilityHandlerRouter(NewAccountCapabilityHandler(service.NewAccountCapabilityService(repo, accounts, nil)), true)
	for attempt := 0; attempt < 2; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(`{"kind":"discover","folder_ids":[7,8],"account_ids":[31]}`))
		request.Header.Set("Idempotency-Key", "stable")
		request.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, request)
		require.Equal(t, http.StatusAccepted, w.Code)
		require.NotContains(t, w.Body.String(), "offline-secret-fixture")
		require.NotContains(t, w.Body.String(), "request_hash")
		if attempt == 1 {
			require.Equal(t, "true", w.Header().Get("Idempotency-Replayed"))
		}
	}
	require.Equal(t, 1, accounts.reads)
	require.Len(t, repo.items, 1)
	require.Equal(t, int64(7), repo.items[0].FolderID)
}

func TestAccountCapabilityJobsHandlerRejectsCredentialsAndMalformedQuery(t *testing.T) {
	router := capabilityHandlerRouter(NewAccountCapabilityHandler(nil), true)
	for _, body := range []string{
		`{"kind":"discover","folder_ids":[7],"account_ids":[31],"api_key":"must-never-be-stored"}`,
		`{"kind":"discover"} {"kind":"probe"}`,
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(body)))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.NotContains(t, w.Body.String(), "must-never-be-stored")
	}
	for _, query := range []string{"folder_ids=7,nope", "account_ids=31,-2", "account_id=bogus"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/inventory?"+query, nil))
		require.Equal(t, http.StatusBadRequest, w.Code)
	}
}

func TestAccountCapabilityJobsHandlerRequiresAuthenticatedActor(t *testing.T) {
	router := capabilityHandlerRouter(NewAccountCapabilityHandler(nil), false)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAccountCapabilityJobsHandlerDoesNotExposeInternalError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	response.ErrorFrom(c, accountCapabilityHTTPError(errors.New("database failure with private credential and upstream body")))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.NotContains(t, w.Body.String(), "private credential")
	require.Contains(t, w.Body.String(), "Capability operation failed")
}
