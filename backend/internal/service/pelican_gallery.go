package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"slices"
)

func (r *PelicanTestRunner) Gallery(ctx context.Context, filter PelicanResultFilter) (*PelicanResultPage, error) {
	if err := r.initialize(ctx); err != nil {
		return nil, err
	}
	reader, ok := r.repo.(PelicanTaskReader)
	if !ok {
		return nil, errors.New("pelican summary repository is unavailable")
	}
	return reader.ListTaskResults(ctx, filter.TaskID, filter)
}

type PelicanRetestRequest struct {
	ClientTaskID string   `json:"client_task_id"`
	ResultIDs    []string `json:"result_ids,omitempty"`
	FailedOnly   bool     `json:"failed_only,omitempty"`
}

func (r *PelicanTestRunner) Retest(ctx context.Context, createdBy int64, id string, request PelicanRetestRequest, catalog *PelicanTestCatalog) (*PelicanTaskSnapshot, error) {
	client, err := uuid.Parse(request.ClientTaskID)
	if err != nil || client.Version() != 7 {
		return nil, ErrPelicanTestInvalidRequest
	}
	request.ClientTaskID = client.String()
	ids := slices.Clone(request.ResultIDs)
	slices.Sort(ids)
	keyBytes, _ := json.Marshal(struct {
		Source string
		IDs    []string
		Failed bool
	}{id, ids, request.FailedOnly})
	keyHash := sha256.Sum256(keyBytes)
	key := hex.EncodeToString(keyHash[:])
	// Repeated retest submissions recover their accepted task before reading a source
	// that the administrator may since have deleted.
	if existing, err := r.TaskSnapshot(ctx, request.ClientTaskID); err == nil {
		if existing.TestConfig.RetestKey != key {
			return nil, ErrPelicanTestReplayConflict
		}
		existing.Replayed = true
		return existing, nil
	} else if !errors.Is(err, ErrPelicanTestNotFound) {
		return nil, err
	}
	task, err := r.TaskSnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	if task.Status == "pending" || task.Status == "running" {
		return nil, ErrPelicanTestRunning
	}
	parameters := PelicanTestRequest{ClientTaskID: request.ClientTaskID, GenerationTimeoutSeconds: task.GenerationTimeoutSeconds, PelicanTestConfig: task.TestConfig}
	parameters.RetestKey = key
	if len(parameters.TestItems) == 0 {
		parameters.TestItems = []string{"pelican"}
	}
	for page := 1; ; page++ {
		rows, readErr := r.TaskResults(ctx, id, PelicanResultFilter{Page: page, Size: 50})
		if readErr != nil {
			return nil, readErr
		}
		for _, row := range rows.Items {
			if len(request.ResultIDs) > 0 && !slices.Contains(request.ResultIDs, row.ID) {
				continue
			}
			failed := row.Status == "failed" || row.Status == "incomplete" || row.Status == "skipped"
			if row.Evaluation != nil {
				failed = failed || row.Evaluation.Verdict == "invalid" || row.Evaluation.Verdict == "error"
			}
			if request.FailedOnly && !failed {
				continue
			}
			target := PelicanTestTarget{AccountID: row.AccountID, ModelID: row.ModelID, Effort: row.Effort}
			if row.Evaluation != nil {
				target.Scene = row.Evaluation.Scene
			}
			parameters.Targets = append(parameters.Targets, target)
		}
		if page*50 >= int(rows.Total) {
			break
		}
	}
	if len(request.ResultIDs) > 0 && len(parameters.Targets) != len(request.ResultIDs) {
		return nil, ErrPelicanTestInvalidRequest
	}
	return r.StartBackground(ctx, createdBy, parameters, catalog)
}
