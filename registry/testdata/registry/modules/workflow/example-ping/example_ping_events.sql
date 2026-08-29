-- name: InsertExamplePingEvent :exec
INSERT INTO example_ping_events (note) VALUES ($1);

-- name: CountExamplePingEvents :one
SELECT COUNT(*) FROM example_ping_events;
