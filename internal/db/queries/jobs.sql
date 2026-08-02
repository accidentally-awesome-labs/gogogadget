-- name: EnqueueJob :one
INSERT INTO jobs (kind, payload, run_at)
VALUES ($1, $2, COALESCE($3, now()))
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

-- name: DeadLetterJob :exec
UPDATE jobs SET done_at = now(), last_error = 'exhausted' WHERE id = $1;

-- name: DeleteOldJobs :exec
DELETE FROM jobs WHERE done_at IS NOT NULL AND done_at < now() - interval '7 days';
