package service

import (
	"context"
	"errors"
	"io"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

const (
	ImageStudioModelGPTImage2      = extensionv1.ImageStudioModelGPTImage2
	ImageStudioModelGeminiProImage = extensionv1.ImageStudioModelGeminiProImage

	ImageStudioMaxOutputCount = extensionv1.ImageStudioMaxOutputCount
	ImageStudioMaxImageBytes  = 20 << 20
	ImageStudioGlobalBytes    = int64(2 << 30)
	ImageStudioUserBytes      = int64(250 << 20)

	ImageStudioFileRetention     = 24 * time.Hour
	ImageStudioMetadataRetention = 30 * 24 * time.Hour
)

type ImageStudioMode string

const (
	ImageStudioModeGenerate ImageStudioMode = "generate"
	ImageStudioModeEdit     ImageStudioMode = "edit"
)

type ImageStudioJobStatus string

const (
	ImageStudioJobPending             ImageStudioJobStatus = "pending"
	ImageStudioJobPreparing           ImageStudioJobStatus = "preparing"
	ImageStudioJobRunning             ImageStudioJobStatus = "running"
	ImageStudioJobSucceeded           ImageStudioJobStatus = "succeeded"
	ImageStudioJobPartiallySucceeded  ImageStudioJobStatus = "partially_succeeded"
	ImageStudioJobFailed              ImageStudioJobStatus = "failed"
	ImageStudioJobCanceled            ImageStudioJobStatus = "canceled"
	ImageStudioJobCanceledWithResults ImageStudioJobStatus = "canceled_with_results"
)

type ImageStudioItemStatus string

const (
	ImageStudioItemPending   ImageStudioItemStatus = "pending"
	ImageStudioItemRunning   ImageStudioItemStatus = "running"
	ImageStudioItemSucceeded ImageStudioItemStatus = "succeeded"
	ImageStudioItemFailed    ImageStudioItemStatus = "failed"
	ImageStudioItemCanceled  ImageStudioItemStatus = "canceled"
)

type ImageStudioArtifactKind string

const (
	ImageStudioArtifactReference ImageStudioArtifactKind = "reference"
	ImageStudioArtifactMask      ImageStudioArtifactKind = "mask"
	ImageStudioArtifactOutput    ImageStudioArtifactKind = "output"
)

type ImageStudioError struct {
	Status  int
	Code    string
	Message string
}

func (e *ImageStudioError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func newImageStudioError(status int, code, message string) error {
	return &ImageStudioError{Status: status, Code: code, Message: message}
}

var (
	ErrImageStudioNotFound       = errors.New("image studio job not found")
	ErrImageStudioNoWork         = errors.New("no image studio work available")
	ErrImageStudioActiveJob      = errors.New("an image studio job is already active")
	ErrImageStudioNotRetryable   = errors.New("image studio job is not retryable")
	ErrImageStudioRequestExpired = errors.New("image studio job input has expired")
)

type ImageStudioCreateInput struct {
	APIKeyID int64
	Mode     ImageStudioMode
	Model    string
	Prompt   string
	Count    int
	Size     string
	Quality  string
}

type ImageStudioCounts struct {
	Processed int `json:"processed"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Canceled  int `json:"canceled"`
}

type ImageStudioJob struct {
	ID                int64                `json:"id"`
	UserID            int64                `json:"-"`
	APIKeyID          int64                `json:"api_key_id"`
	Mode              ImageStudioMode      `json:"mode"`
	Model             string               `json:"model"`
	Prompt            string               `json:"-"`
	Size              string               `json:"size,omitempty"`
	Quality           string               `json:"quality,omitempty"`
	Count             int                  `json:"count"`
	Status            ImageStudioJobStatus `json:"status"`
	Counts            ImageStudioCounts    `json:"counts"`
	CancelRequestedAt *time.Time           `json:"cancel_requested_at,omitempty"`
	ErrorCode         string               `json:"error_code,omitempty"`
	ErrorMessage      string               `json:"error_message,omitempty"`
	RequestExpiresAt  time.Time            `json:"request_expires_at"`
	RetainUntil       time.Time            `json:"retain_until"`
	StartedAt         *time.Time           `json:"started_at,omitempty"`
	FinishedAt        *time.Time           `json:"finished_at,omitempty"`
	CreatedAt         time.Time            `json:"created_at"`
	UpdatedAt         time.Time            `json:"updated_at"`
}

func (j ImageStudioJob) Terminal() bool {
	switch j.Status {
	case ImageStudioJobSucceeded, ImageStudioJobPartiallySucceeded, ImageStudioJobFailed,
		ImageStudioJobCanceled, ImageStudioJobCanceledWithResults:
		return true
	default:
		return false
	}
}

type ImageStudioItem struct {
	ID           int64                 `json:"id"`
	JobID        int64                 `json:"job_id"`
	Ordinal      int                   `json:"ordinal"`
	Status       ImageStudioItemStatus `json:"status"`
	ErrorCode    string                `json:"error_code,omitempty"`
	ErrorMessage string                `json:"error_message,omitempty"`
	StartedAt    *time.Time            `json:"started_at,omitempty"`
	FinishedAt   *time.Time            `json:"finished_at,omitempty"`
	CreatedAt    time.Time             `json:"created_at"`
	UpdatedAt    time.Time             `json:"updated_at"`
}

type ImageStudioArtifact struct {
	ID            int64                   `json:"id"`
	JobID         int64                   `json:"job_id"`
	ItemID        *int64                  `json:"item_id,omitempty"`
	Kind          ImageStudioArtifactKind `json:"kind"`
	StorageKey    string                  `json:"-"`
	ContentType   string                  `json:"content_type"`
	ByteSize      int64                   `json:"byte_size"`
	RevisedPrompt string                  `json:"revised_prompt,omitempty"`
	ExpiresAt     time.Time               `json:"expires_at"`
	CreatedAt     time.Time               `json:"created_at"`
}

type ImageStudioInputArtifact struct {
	Kind        ImageStudioArtifactKind
	StorageKey  string
	ContentType string
	ByteSize    int64
}

type ImageStudioCreateParams struct {
	UserID           int64
	Input            ImageStudioCreateInput
	RequestExpiresAt time.Time
	RetainUntil      time.Time
	InputArtifacts   []ImageStudioInputArtifact
}

type ImageStudioClaim struct {
	Job    ImageStudioJob
	Item   ImageStudioItem
	Inputs []ImageStudioArtifact
}

type ImageStudioExecutionRequest struct {
	Job                  ImageStudioJob
	Item                 ImageStudioItem
	APIKey               *APIKey
	Reference            []byte
	ReferenceContentType string
	Mask                 []byte
	MaskContentType      string
}

type ImageStudioExecutionResult struct {
	Data          []byte
	ContentType   string
	RevisedPrompt string
}

type ImageStudioExecutor interface {
	Execute(ctx context.Context, request ImageStudioExecutionRequest) (*ImageStudioExecutionResult, error)
}

type ImageStudioFileStorage interface {
	Save(ctx context.Context, userID int64, kind ImageStudioArtifactKind, data []byte, contentType string, expiresAt time.Time) (ImageStudioInputArtifact, error)
	Open(storageKey string) (io.ReadCloser, error)
	Read(storageKey string) ([]byte, error)
	Remove(storageKey string) error
}

type ImageStudioStorageUsage struct {
	Global int64
	User   int64
}

type ImageStudioRepository interface {
	Create(ctx context.Context, params ImageStudioCreateParams) (*ImageStudioJob, error)
	Get(ctx context.Context, userID, jobID int64) (*ImageStudioJob, error)
	List(ctx context.Context, userID int64, limit, offset int) ([]ImageStudioJob, error)
	ListItems(ctx context.Context, userID, jobID int64) ([]ImageStudioItem, error)
	ListOutputArtifacts(ctx context.Context, userID, jobID int64) ([]ImageStudioArtifact, error)
	GetArtifact(ctx context.Context, userID, jobID, artifactID int64) (*ImageStudioArtifact, error)
	RequestCancel(ctx context.Context, userID, jobID int64) (*ImageStudioJob, error)
	Retry(ctx context.Context, userID, jobID int64, now time.Time) (*ImageStudioJob, error)
	RecoverInterrupted(ctx context.Context) error
	ClaimNext(ctx context.Context) (*ImageStudioClaim, error)
	CompleteSuccess(ctx context.Context, itemID int64, artifact ImageStudioInputArtifact, revisedPrompt string, expiresAt time.Time) error
	CompleteFailure(ctx context.Context, itemID int64, code, message string) error
	Finalize(ctx context.Context, jobID int64) (*ImageStudioJob, error)
	StorageUsage(ctx context.Context, userID int64, now time.Time) (ImageStudioStorageUsage, error)
	ListExpiredArtifacts(ctx context.Context, now time.Time, limit int) ([]ImageStudioArtifact, error)
	DeleteArtifact(ctx context.Context, artifactID int64) error
	ExpireRequests(ctx context.Context, now time.Time) error
	DeleteExpiredJobs(ctx context.Context, now time.Time) error
}

func ValidateImageStudioCreateInput(input ImageStudioCreateInput, hasReference, hasMask bool) error {
	_, err := planImageStudio(context.Background(), input, hasReference, hasMask, false)
	return err
}

func ResolveImageStudioTerminalStatus(cancelRequested bool, counts ImageStudioCounts) ImageStudioJobStatus {
	if cancelRequested {
		if counts.Succeeded > 0 {
			return ImageStudioJobCanceledWithResults
		}
		return ImageStudioJobCanceled
	}
	if counts.Succeeded == counts.Processed && counts.Processed > 0 {
		return ImageStudioJobSucceeded
	}
	if counts.Succeeded > 0 {
		return ImageStudioJobPartiallySucceeded
	}
	return ImageStudioJobFailed
}
