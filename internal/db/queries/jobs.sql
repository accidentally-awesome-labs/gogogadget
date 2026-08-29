-- max_attempts is written at enqueue time from the declaring module's budget,
-- and the row is dispatch truth from then on: a job queued before a module
-- changed its budget keeps the one it was enqueued under rather than silently
-- adopting the new value mid-flight. 0 falls back to the column default.
-- name: EnqueueJob :one
INSERT INTO jobs (kind, payload, run_at, max_attempts)
VALUES ($1, $2, COALESCE(sqlc.arg(run_at), now()),
        COALESCE(NULLIF(sqlc.arg(max_attempts)::int, 0), 8))
RETURNING id;

-- Claim with a 5-minute visibility timeout: a crashed worker's job reappears
-- instead of being double-claimed mid-flight. Multi-node safe (SKIP LOCKED).
-- pgx.ErrNoRows ⇒ queue empty ⇒ worker sleeps.
-- name: ClaimJob :one
UPDATE jobs
SET attempts = attempts + 1, last_error = NULL, run_at = now() + interval '5 minutes'
WHERE id = (
  SELECT id FROM jobs
  WHERE done_at IS NULL AND run_at <= now()
  ORDER BY run_at
  LIMIT 1
  FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: CompleteJob :exec
UPDATE jobs SET done_at = now() WHERE id = $1;

-- Exponential backoff: 2^attempts minutes.
-- name: FailJob :exec
UPDATE jobs SET last_error = $2, run_at = now() + (interval '1 minute' * power(2, attempts))
WHERE id = $1;

-- Dead-letter with an explicit reason. 'exhausted' means the handler kept
-- failing; 'module_uninstalled' means no module provides the kind any more, so
-- retrying is pointless. Both are terminal and both stay requeueable, because
-- reinstalling the module makes the queued work recoverable.
-- name: DeadLetterJob :exec
UPDATE jobs SET done_at = now(), last_error = sqlc.arg(reason) WHERE id = $1;

-- name: DeleteOldJobs :exec
DELETE FROM jobs WHERE done_at IS NOT NULL AND done_at < now() - interval '7 days';

-- Admin jobs viewer. A claimed job and a backoff-retry job both read
-- 'retrying': the 5-min visibility lease is indistinguishable from backoff
-- by design.
-- name: ListJobs :many
SELECT *, CASE
  WHEN done_at IS NULL AND attempts = 0 THEN 'pending'
  WHEN done_at IS NULL AND run_at > now() THEN 'retrying'
  WHEN done_at IS NULL THEN 'running'
  WHEN last_error IN ('exhausted', 'module_uninstalled') THEN 'dead'
  ELSE 'done'
END AS status
FROM jobs
WHERE (sqlc.arg(filter)::text = '' OR kind ILIKE '%' || sqlc.arg(filter) || '%')
ORDER BY created_at DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: CountJobs :one
SELECT count(*) FROM jobs
WHERE (sqlc.arg(filter)::text = '' OR kind ILIKE '%' || sqlc.arg(filter) || '%');

-- Dead-letter requeue: resets the row so ClaimJob picks it up immediately.
-- The guard clause makes a double-requeue a no-op instead of reviving a job
-- that already ran again.
-- name: RequeueDeadJob :exec
UPDATE jobs SET done_at = NULL, attempts = 0, last_error = NULL, run_at = now()
WHERE id = $1 AND done_at IS NOT NULL AND last_error IN ('exhausted', 'module_uninstalled');
