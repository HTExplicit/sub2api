package service

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// Each runtime owns one shared pool, including when two administrators submit
// tests at once. Import workers retain their ordered identity-resolution path.
func (r *AccountJobRuntime) executeBatchTests(parent context.Context, job *AccountJob, payload json.RawMessage) (string, string) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	failures := make(chan string, 1)
	fail := func(code string) {
		select {
		case failures <- code:
		default:
		}
		cancel()
	}
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				requested, err := r.jobs.repo.CancelRequested(ctx, job.ID)
				if err != nil {
					if ctx.Err() == nil {
						fail("cancel_check_failed")
					}
					return
				}
				if requested {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-monitorDone }()
	for ctx.Err() == nil {
		items, err := r.jobs.repo.ReservePendingItems(ctx, job.ID, AccountJobBatchSize)
		if err != nil {
			if ctx.Err() == nil {
				fail("item_reservation_failed")
			}
			break
		}
		if len(items) == 0 {
			break
		}
		queue := make(chan AccountJobItem, len(items))
		for _, item := range items {
			queue <- item
		}
		close(queue)
		var wg sync.WaitGroup
		for range min(5, len(items)) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for item := range queue {
					result := AccountJobExecutionResult{ItemID: item.ID, Status: AccountJobItemStatusCanceled}
					select {
					case r.batchTestSlots <- struct{}{}:
						if ctx.Err() == nil {
							result = r.executeItem(ctx, job, payload, item)
						}
						<-r.batchTestSlots
					case <-ctx.Done():
					}
					if err := r.jobs.repo.CompleteItems(r.ctx, job.ID, []AccountJobExecutionResult{result}); err != nil {
						fail("item_completion_failed")
						return
					}
				}
			}()
		}
		wg.Wait()
	}
	cancel()
	<-monitorDone
	select {
	case code := <-failures:
		return normalizeAccountJobFailure(code)
	default:
		return "", ""
	}
}

func (r *AccountJobRuntime) executeItem(ctx context.Context, job *AccountJob, payload json.RawMessage, item AccountJobItem) AccountJobExecutionResult {
	result := AccountJobExecutionResult{ItemID: item.ID, Status: AccountJobItemStatusFailed,
		ErrorCode: "execution_failed", ErrorMessage: "account job item failed"}
	if r.executor != nil {
		results, err := r.executor.ExecuteAccountJob(ctx, job, payload, []AccountJobItem{item})
		if err == nil && len(results) == 1 && results[0].ItemID == item.ID {
			result = results[0]
		}
	}
	if err := ValidateAccountJobMetadata(result.Metadata); err != nil {
		result = AccountJobExecutionResult{ItemID: item.ID, Status: AccountJobItemStatusFailed,
			ErrorCode: "result_redacted", ErrorMessage: "account job result was rejected"}
	}
	if result.Status == AccountJobItemStatusFailed {
		result.ErrorCode, result.ErrorMessage = normalizeAccountJobFailure(result.ErrorCode)
	} else {
		result.ErrorCode, result.ErrorMessage = "", ""
	}
	return result
}
