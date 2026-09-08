package repository

// capabilityPublicationSupersededSQL is shared by publication's locked evidence
// snapshot and the management inventory. Its outer item alias is i. It queries
// the complete ledger rather than a latest-item projection: a later catalog
// success can retire an old credential failure without reviving inference
// evidence that preceded that failure. Only a fresh inference probe can do so.
// kind belongs to the run, never to admin_capability_items.
const capabilityPublicationSupersededSQL = `EXISTS(
 SELECT 1 FROM admin_capability_items n
 JOIN admin_capability_runs nr ON nr.id=n.run_id
 WHERE n.account_id=i.account_id AND n.config_fingerprint=i.config_fingerprint
 AND (n.finished_at>i.finished_at OR (n.finished_at=i.finished_at AND n.id>i.id))
 AND (
  (n.upstream_model=i.upstream_model AND n.protocol=i.protocol AND n.profile=i.profile
   AND n.status IN ('failed','indeterminate','stale'))
  OR (n.status='failed' AND n.result->>'account_failure'='true')
  OR (n.status='succeeded' AND i.result->>'account_failure'='true'
   AND (n.result->>'status'='alive'
    OR (nr.kind='discover' AND n.result->>'source'='upstream'
     AND n.result->>'status' IN ('discovered','empty','partial'))))
 ))`
