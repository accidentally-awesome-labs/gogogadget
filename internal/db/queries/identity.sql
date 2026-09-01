-- name: GetIdentitySubject :one
SELECT * FROM identity_subjects WHERE provider = $1 AND subject = $2;

-- name: InsertIdentitySubject :one
INSERT INTO identity_subjects (provider, subject, user_id)
VALUES ($1, $2, $3)
ON CONFLICT (provider, subject) DO NOTHING
RETURNING *;

-- name: GetIdentityOrganization :one
SELECT * FROM identity_organizations WHERE provider = $1 AND subject = $2;

-- name: InsertIdentityOrganization :one
INSERT INTO identity_organizations (provider, subject, org_id)
VALUES ($1, $2, $3)
ON CONFLICT (provider, subject) DO NOTHING
RETURNING *;

-- name: GetIdentitySubjectByUser :one
SELECT * FROM identity_subjects WHERE user_id = $1 ORDER BY provider, subject LIMIT 1;

-- name: GetIdentityOrganizationByOrg :one
SELECT * FROM identity_organizations WHERE org_id = $1 ORDER BY provider, subject LIMIT 1;


-- name: GetBillingAccount :one
SELECT * FROM billing_accounts WHERE provider = $1 AND provider_customer_id = $2;

-- name: InsertBillingAccount :one
INSERT INTO billing_accounts (provider, provider_customer_id, org_id)
VALUES ($1, $2, $3)
ON CONFLICT (provider, provider_customer_id) DO NOTHING
RETURNING *;
