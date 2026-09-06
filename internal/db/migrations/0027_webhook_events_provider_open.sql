-- +goose Up
-- webhook_events is the SHARED idempotency ledger: identity deliveries and
-- billing deliveries both record their provider's delivery id here, and the
-- receivers that write it are provider-neutral by design.
--
-- Its provider column was a closed whitelist — ('clerk','polar','local','dev')
-- — which is a list of vendor names embedded in generic data. Any provider
-- outside it makes a correct, verified delivery fail with a constraint
-- violation the receiver can only report as a 500: a third-party identity or
-- billing adapter published from another registry cannot record idempotency at
-- all, and neither can a seam's own test double. The whitelist was found by
-- the doubles, but it was never only about them.
--
-- The invariant that actually matters is that the provider is NAMED, so a
-- delivery id is unique per provider rather than globally. subscriptions
-- already states exactly that (subscriptions_provider_not_empty), so this
-- follows the precedent in the same schema instead of inventing a second
-- convention.
ALTER TABLE webhook_events DROP CONSTRAINT webhook_events_provider_check;
ALTER TABLE webhook_events ADD CONSTRAINT webhook_events_provider_not_empty CHECK (provider <> '');

-- +goose Down
ALTER TABLE webhook_events DROP CONSTRAINT webhook_events_provider_not_empty;
ALTER TABLE webhook_events ADD CONSTRAINT webhook_events_provider_check CHECK (provider IN ('clerk','polar','local','dev'));
