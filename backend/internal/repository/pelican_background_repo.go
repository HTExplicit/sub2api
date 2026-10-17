package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.PelicanTaskReader = (*pelicanTestRepository)(nil)

func (r *pelicanTestRepository) GetTaskSnapshot(ctx context.Context, id string) (*service.PelicanTaskSnapshot, error) {
	task, err := scanPelicanTestTask(r.db.QueryRowContext(ctx, `SELECT `+pelicanTestTaskColumns+`
 FROM codex_gateway_borrow_test_tasks WHERE scope='manual_pelican' AND (id=$1 OR client_task_id=$1) AND expires_at>CURRENT_TIMESTAMP`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrPelicanTestNotFound
	}
	if err != nil {
		return nil, err
	}
	if task.DeletedAt != nil {
		return nil, service.ErrPelicanTestDeleted
	}
	snapshot := &service.PelicanTaskSnapshot{PelicanTestTask: task, Counts: make(map[string]int)}
	rows, err := r.db.QueryContext(ctx, `SELECT status, COUNT(*), COUNT(*) FILTER (WHERE status='complete' AND octet_length(html)=0),
 COUNT(*) FILTER (WHERE status IN ('failed','incomplete','skipped') OR evaluation->>'verdict' IN ('invalid','error'))
 FROM codex_gateway_borrow_test_results WHERE task_id=$1 GROUP BY status`, task.ID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var status string
		var count, noPreview, failed int
		if err := rows.Scan(&status, &count, &noPreview, &failed); err != nil {
			return nil, err
		}
		snapshot.Counts[status] = count
		snapshot.Counts["no_preview"] += noPreview
		snapshot.FailedCount += failed
	}
	snapshot.Completed = 0
	for status, count := range snapshot.Counts {
		if status != "pending" && status != "running" && status != "no_preview" {
			snapshot.Completed += count
		}
	}
	return snapshot, rows.Err()
}

// Keep this projection independent from pelicanTestResultColumns:
// no raw_answer, raw_response, raw_html, html or complete error enters a page.
const pelicanResultSummaryColumns = `r.id,r.task_id,r.ordinal,r.account_id,r.account_name,r.platform,r.model_id,r.effort,r.status,
 (octet_length(r.raw_svg)>0 OR r.status='complete' AND octet_length(r.html)>0),
 (r.status='incomplete' AND t.error=convert_to('process stopped before the test finished; this task will not be replayed','UTF8')),
 r.started_at,r.finished_at,r.generation_started_at,r.duration_ms,r.queue_duration_ms,r.preparation_duration_ms,r.generation_duration_ms,t.expires_at,r.evaluation,r.repeat_index,COALESCE((t.test_config->>'repeat_count')::integer,1)`

func (r *pelicanTestRepository) ListTaskResults(ctx context.Context, id string, filter service.PelicanResultFilter) (*service.PelicanResultPage, error) {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.Size < 1 {
		filter.Size = 24
	}
	if filter.Size > 50 {
		filter.Size = 50
	}
	args := []any{}
	where := []string{"t.scope='manual_pelican'", "t.deleted_at IS NULL", "t.expires_at>CURRENT_TIMESTAMP"}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if id != "" {
		add("t.id=$%d", id)
	}
	if filter.Effort != "" {
		add("r.effort=$%d", filter.Effort)
	}
	if filter.Scene != "" {
		add("r.evaluation->>'scene'=$%d", filter.Scene)
	}
	if filter.Verdict != "" {
		add("r.evaluation->>'verdict'=$%d", filter.Verdict)
	}
	if filter.AccountID > 0 {
		add("r.account_id=$%d", filter.AccountID)
	}
	if filter.Model != "" {
		add("r.model_id=$%d", filter.Model)
	}
	switch filter.Status {
	case "":
	case "no_preview":
		where = append(where, "r.status='complete' AND octet_length(r.html)=0")
	case "pending", "running", "complete", "failed", "incomplete", "cancelled", "skipped":
		add("r.status=$%d", filter.Status)
	default:
		return nil, service.ErrPelicanTestInvalidRequest
	}
	from := ` FROM codex_gateway_borrow_test_results r JOIN codex_gateway_borrow_test_tasks t ON t.id=r.task_id WHERE ` + strings.Join(where, " AND ")
	page := &service.PelicanResultPage{Items: []*service.PelicanResultSummary{}, Page: filter.Page, PageSize: filter.Size}
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*)"+from, args...).Scan(&page.Total); err != nil {
		return nil, err
	}
	args = append(args, filter.Size, (filter.Page-1)*filter.Size)
	order := "r.ordinal"
	if id == "" {
		order = "COALESCE(r.started_at,t.created_at) DESC,r.id DESC"
	}
	query := "SELECT " + pelicanResultSummaryColumns + from + fmt.Sprintf(" ORDER BY %s LIMIT $%d OFFSET $%d", order, len(args)-1, len(args))
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		item := &service.PelicanResultSummary{}
		var evaluation []byte
		if err := rows.Scan(&item.ID, &item.TaskID, &item.Ordinal, &item.AccountID, &item.AccountName, &item.Platform, &item.ModelID, &item.Effort, &item.Status,
			&item.HasPreview, &item.Interrupted, &item.StartedAt, &item.FinishedAt, &item.GenerationStartedAt, &item.DurationMS, &item.QueueDurationMS, &item.PreparationDurationMS, &item.GenerationDurationMS, &item.ExpiresAt, &evaluation, &item.RepeatIndex, &item.RepeatCount); err != nil {
			return nil, err
		}
		if len(evaluation) > 0 {
			if err = json.Unmarshal(evaluation, &item.Evaluation); err != nil {
				return nil, err
			}
		}
		page.Items = append(page.Items, item)
	}
	return page, rows.Err()
}
