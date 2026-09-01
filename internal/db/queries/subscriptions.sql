-- name: GetSubscriptionByOrg :one
SELECT * FROM subscriptions WHERE org_id = $1;

-- Resubscribe after cancellation arrives with a NEW provider_subscription_id,
-- so org_id is the only safe conflict target.
-- name: UpsertSubscription :one
INSERT INTO subscriptions (org_id, provider, provider_subscription_id, provider_customer_id, product_key, status, current_period_end, cancel_at_period_end)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (org_id) DO UPDATE
SET provider = EXCLUDED.provider,
    provider_subscription_id = EXCLUDED.provider_subscription_id,
    product_key = EXCLUDED.product_key,
    status = EXCLUDED.status,
    current_period_end = EXCLUDED.current_period_end,
    cancel_at_period_end = EXCLUDED.cancel_at_period_end,
    updated_at = now()
RETURNING *;

-- name: CountActiveSubscriptions :one
SELECT count(*) FROM subscriptions WHERE status IN ('active', 'trialing', 'past_due');

-- name: ListRevenueSubscriptions :many
SELECT product_key, count(*) AS n FROM subscriptions
WHERE status IN ('active', 'trialing', 'past_due')
GROUP BY product_key;
