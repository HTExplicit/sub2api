package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrCindyAccountRequired           = infraerrors.BadRequest("CINDY_ACCOUNT_REQUIRED", "account is not a Cindy API key account")
	ErrCindyInsufficientDeleteChanged = infraerrors.Conflict(
		"CINDY_INSUFFICIENT_DELETE_CHANGED",
		"matching Cindy accounts changed; reload and confirm again",
	)
)

type CindyInsufficientDeletePreview struct {
	Count       int    `json:"count"`
	Fingerprint string `json:"fingerprint"`
}

type CindyInsufficientDeleteResult struct {
	DeletedCount      int     `json:"deleted_count"`
	DeletedAccountIDs []int64 `json:"-"`
}

// CindyBalanceAccountRepository is intentionally separate from AccountRepository
// so focused gateway test doubles do not need to implement administrative cleanup.
type CindyBalanceAccountRepository interface {
	MarkCindyBalanceInsufficient(ctx context.Context, accountID int64, observedAt time.Time) (bool, error)
	ClearCindyBalanceInsufficient(ctx context.Context, accountID int64) (bool, error)
	PreviewCindyInsufficientDeletion(ctx context.Context) (*CindyInsufficientDeletePreview, error)
	DeleteCindyInsufficient(ctx context.Context, expectedCount int, fingerprint string) (*CindyInsufficientDeleteResult, error)
}

func CindyInsufficientAccountFingerprint(accountIDs []int64) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("cindy-insufficient:v1\n"))
	for _, accountID := range accountIDs {
		_, _ = hash.Write([]byte(strconv.FormatInt(accountID, 10)))
		_, _ = hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func NormalizeCindyInsufficientFingerprint(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
