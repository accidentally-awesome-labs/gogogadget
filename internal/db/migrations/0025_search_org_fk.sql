-- +goose Up
ALTER TABLE search_documents ADD CONSTRAINT search_documents_tenant_fk FOREIGN KEY (tenant_id) REFERENCES orgs(org_id) ON DELETE CASCADE;
ALTER TABLE search_outbox ADD CONSTRAINT search_outbox_tenant_fk FOREIGN KEY (tenant_id) REFERENCES orgs(org_id) ON DELETE CASCADE;
-- +goose Down
ALTER TABLE search_outbox DROP CONSTRAINT search_outbox_tenant_fk;
ALTER TABLE search_documents DROP CONSTRAINT search_documents_tenant_fk;
