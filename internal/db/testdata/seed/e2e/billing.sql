-- Pro org: active subscription ending in 30 days (relative to seed time).
INSERT INTO subscriptions (clerk_org_id, polar_subscription_id, polar_customer_id, product_key, status, current_period_end)
VALUES ('org_pro', 'sub_e2e_pro', 'cust_e2e_pro', 'pro', 'active', now() + interval '30 days')
ON CONFLICT (clerk_org_id) DO NOTHING;
