package repository

// capabilityPublicationSupersededSQL is shared by publication's locked evidence
// snapshot and the management inventory. Its outer item alias is i. It queries
// the complete ledger rather than a latest-item projection: a later catalog
// success can retire an old credential failure without reviving inference
// evidence that preceded that failure. Temporary failures never erase an older
// success. Basic text, tool roundtrips and token counting stay separate; only
// basic text or discovery can establish/retire an account-wide failure here.
// kind belongs to the run, never to admin_capability_items.
const capabilityPublicationSupersededSQL = `EXISTS(
 SELECT 1 FROM admin_capability_items n
 JOIN admin_capability_runs nr ON nr.id=n.run_id
 WHERE n.account_id=i.account_id AND n.folder_id=i.folder_id AND n.config_fingerprint=i.config_fingerprint
 AND (n.finished_at>i.finished_at OR (n.finished_at=i.finished_at AND n.id>i.id))
 AND (
  (n.upstream_model=i.upstream_model AND n.protocol=i.protocol AND n.profile=i.profile
   AND i.result->>'account_failure' IS DISTINCT FROM 'true'
   AND nr.kind='probe' AND n.status='failed'
   AND n.result->>'classification' IN ('model_unavailable','protocol_unsupported'))
  OR (n.status='failed' AND n.result->>'account_failure'='true'
   AND (nr.kind='discover' OR (nr.kind='probe' AND n.profile='text'
    AND n.protocol IN ('responses','chat_completions','messages','responses_websocket'))))
  OR (n.status='succeeded' AND i.result->>'account_failure'='true'
   AND ((nr.kind='probe' AND n.profile='text' AND n.result->>'status'='alive'
     AND n.protocol IN ('responses','chat_completions','messages','responses_websocket'))
    OR (nr.kind='discover' AND n.result->>'source'='upstream'
     AND n.result->>'status' IN ('discovered','empty','partial'))))
 ))`
