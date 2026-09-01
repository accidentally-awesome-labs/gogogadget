-- +goose Up
ALTER TABLE webhook_outbox ADD CONSTRAINT webhook_outbox_org_fk FOREIGN KEY (org_id) REFERENCES orgs(org_id) ON DELETE CASCADE;
-- +goose Down
ALTER TABLE webhook_outbox DROP CONSTRAINT webhook_outbox_org_fk;
