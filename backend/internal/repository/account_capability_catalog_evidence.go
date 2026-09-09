package repository

import (
	"context"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.AccountCapabilityCatalogEvidenceRepository = (*accountCapabilityRepository)(nil)

// EvidenceItems is a read-only projection for the management catalog. It keeps
// the last attempt AND last successful observation for each configuration, so a
// later temporary outage cannot make a previously verified line look untested.
// Pending/running rows are included to prevent duplicate plans. The ordinary
// LatestItems history endpoint retains its original latest-terminal semantics.
func (r *accountCapabilityRepository) EvidenceItems(ctx context.Context, filter service.AccountCapabilityFilter) (*service.AccountCapabilityItemPage, error) {
	filter = capabilityPage(filter)
	scopeFilter := filter
	scopeFilter.Status = ""
	args := []any{}
	scopeWhere := capabilityWhere(scopeFilter, true, &args)
	cte := `WITH scoped AS (
 SELECT i.*,r.kind AS observation_kind
 FROM admin_capability_items i JOIN admin_capability_runs r ON r.id=i.run_id
 WHERE ` + scopeWhere + `
), latest_attempts AS (
 SELECT DISTINCT ON (account_id,folder_id,config_fingerprint,upstream_model,protocol,profile) id
 FROM scoped WHERE NOT (status='canceled' AND dispatched_at IS NULL AND request_count=0
  AND result->>'request_count_unknown' IS DISTINCT FROM 'true')
 ORDER BY account_id,folder_id,config_fingerprint,upstream_model,protocol,profile,
 COALESCE(finished_at,created_at) DESC,id DESC
), last_successes AS (
 SELECT DISTINCT ON (account_id,folder_id,config_fingerprint,upstream_model,protocol,profile) id
 FROM scoped WHERE status='succeeded' AND
 (result->>'status'='alive' OR (observation_kind='discover'
  AND jsonb_typeof(result->'models')='array' AND result->'models'<>'[]'::jsonb))
 ORDER BY account_id,folder_id,config_fingerprint,upstream_model,protocol,profile,
 finished_at DESC NULLS LAST,id DESC
), selected_ids AS (
 SELECT id FROM latest_attempts UNION SELECT id FROM last_successes
) `
	from := `admin_capability_items i JOIN admin_capability_runs r ON r.id=i.run_id
 JOIN selected_ids selected ON selected.id=i.id`
	where := "TRUE"
	if filter.Status != "" {
		args = append(args, filter.Status)
		where += " AND i.status=$" + strconv.Itoa(len(args))
	}
	page := &service.AccountCapabilityItemPage{Items: []service.AccountCapabilityItem{}, Page: filter.Page, PageSize: filter.PageSize}
	if err := r.db.QueryRowContext(ctx, cte+`SELECT COUNT(*) FROM `+from+` WHERE `+where, args...).Scan(&page.Total); err != nil {
		return nil, err
	}
	args = append(args, filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := r.db.QueryContext(ctx, cte+`SELECT `+capabilityItemColumns+` FROM `+from+` WHERE `+where+` ORDER BY i.id DESC LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		item, scanErr := scanCapabilityItem(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		page.Items = append(page.Items, *item)
	}
	return page, rows.Err()
}
