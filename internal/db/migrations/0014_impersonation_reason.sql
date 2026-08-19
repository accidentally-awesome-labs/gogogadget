-- +goose Up
-- Reason capture: impersonation requires a stated purpose. The reason lives
-- on the session row AND in both audit entries (audit_log has no FKs, so the
-- trail survives the session row and the accounts involved).
ALTER TABLE impersonation_sessions ADD COLUMN reason TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE impersonation_sessions DROP COLUMN IF EXISTS reason;
