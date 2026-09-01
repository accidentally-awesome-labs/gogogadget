-- schedules (recurring work — claimed by the jobs worker each poll cycle)

-- name: CreateSchedule :one
INSERT INTO schedules (name, kind, payload, org_id, every_seconds, next_run_at)
VALUES ($1, $2, $3, $4, $5, COALESCE(sqlc.narg(next_run_at)::timestamptz, now()))
RETURNING *;

-- name: ListSchedules :many
SELECT * FROM schedules ORDER BY name;

-- name: GetSchedule :one
SELECT * FROM schedules WHERE id = $1;

-- name: SetScheduleEnabled :exec
UPDATE schedules SET enabled = $2, updated_at = now() WHERE id = $1;

-- name: DeleteSchedule :exec
DELETE FROM schedules WHERE id = $1;

-- name: ClaimDueSchedules :many
-- Advances next_run_at to now()+interval in the same statement: missed ticks
-- are skipped by design (the next fire is now+interval, not catch-up).
UPDATE schedules
SET last_run_at = now(), next_run_at = now() + make_interval(secs => every_seconds), updated_at = now()
WHERE id IN (
  SELECT id FROM schedules
  WHERE enabled AND next_run_at <= now()
  ORDER BY next_run_at
  FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: RunScheduleNow :exec
-- Fires the schedule on the next scheduler pass (worker polls every ~2s).
-- Guarded on enabled so a disabled row can't be sneak-fired.
UPDATE schedules SET next_run_at = now(), updated_at = now() WHERE id = $1 AND enabled;
