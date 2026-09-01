package jobs

func workerDefinitions(w *Worker) []Definition {
	d0 := w.defineEmailDigest()
	d1 := w.defineEmailDunningFinal()
	d2 := w.defineEmailDunningReminder()
	d3 := w.defineEmailPaymentFailed()
	d4 := w.defineEmailSubscriptionCanceled()
	d5 := w.defineEmailTrialEnding()
	d6 := w.defineEmailWelcome()
	d7 := w.defineExportOrgJSON()
	d8 := w.defineExportProjectsCSV()
	d9 := w.defineUsageFlush()
	d10 := w.defineWebhookDeliver()
	return []Definition{
		d0,
		d1,
		d2,
		d3,
		d4,
		d5,
		d6,
		d7,
		d8,
		d9,
		d10,
	}
}

// SchedulableKinds are the kinds a schedule row may reference. Derived from
// the declarations, so a kind cannot be scheduled unless its module said it
// may be — a schedule pointing at a one-shot handler would fire it forever.
var SchedulableKinds = []string{
	"email.digest",
	"usage.flush",
}

// workerJanitors is every declared cleanup sweep.
func workerJanitors(w *Worker) []Janitor {
	return []Janitor{
		{Name: "audit_log", Sweep: w.janitorAuditLog},
		{Name: "idempotency_keys", Sweep: w.janitorIdempotencyKeys},
		{Name: "old_jobs", Sweep: w.janitorOldJobs},
		{Name: "webhook_events", Sweep: w.janitorWebhookEvents},
		{Name: "webhook_secrets", Sweep: w.janitorWebhookSecrets},
	}
}

var declaredAttempts = map[string]int{
	"email.digest":                8,
	"email.dunning_final":         8,
	"email.dunning_reminder":      8,
	"email.payment_failed":        8,
	"email.subscription_canceled": 8,
	"email.trial_ending":          8,
	"email.welcome":               8,
	"export.org_json":             8,
	"export.projects_csv":         8,
	"usage.flush":                 8,
	"webhook.deliver":             8,
}

func providerActive(env, slot, adapter string) bool {
	switch env {
	case "development":
		if slot == "ggg/analytics" && adapter == "ggg/system/analytics-noop" {
			return true
		}
		if slot == "ggg/audit-export" && adapter == "ggg/system/audit-export-noop" {
			return true
		}
		if slot == "ggg/cache" && adapter == "ggg/system/cache-memory" {
			return true
		}
		if slot == "ggg/billing" && adapter == "ggg/system/billing-local" {
			return true
		}
		if slot == "ggg/database" && adapter == "ggg/system/database-postgres" {
			return true
		}
		if slot == "ggg/feature-flags" && adapter == "ggg/system/feature-flags-postgres" {
			return true
		}
		if slot == "ggg/identity" && adapter == "ggg/system/identity-dev" {
			return true
		}
		if slot == "ggg/llm" && adapter == "ggg/system/llm-fake" {
			return true
		}
		if slot == "ggg/mail" && adapter == "ggg/system/mail-dev" {
			return true
		}
		if slot == "ggg/notifications" && adapter == "ggg/system/notifications-postgres" {
			return true
		}
		if slot == "ggg/observability" && adapter == "ggg/system/observability-log" {
			return true
		}
		if slot == "ggg/rate-limit" && adapter == "ggg/system/rate-limit-memory" {
			return true
		}
		if slot == "ggg/realtime" && adapter == "ggg/system/realtime-postgres" {
			return true
		}
		if slot == "ggg/search" && adapter == "ggg/system/search-postgres" {
			return true
		}
		if slot == "ggg/storage" && adapter == "ggg/system/storage-filesystem" {
			return true
		}
		if slot == "ggg/telemetry" && adapter == "ggg/system/telemetry-noop" {
			return true
		}
		if slot == "ggg/usage" && adapter == "ggg/system/usage-postgres" {
			return true
		}
		if slot == "ggg/webhooks" && adapter == "ggg/system/webhooks-postgres" {
			return true
		}
	case "test":
		if slot == "ggg/analytics" && adapter == "ggg/system/analytics-noop" {
			return true
		}
		if slot == "ggg/audit-export" && adapter == "ggg/system/audit-export-noop" {
			return true
		}
		if slot == "ggg/cache" && adapter == "ggg/system/cache-memory" {
			return true
		}
		if slot == "ggg/billing" && adapter == "ggg/system/billing-local" {
			return true
		}
		if slot == "ggg/database" && adapter == "ggg/system/database-postgres" {
			return true
		}
		if slot == "ggg/feature-flags" && adapter == "ggg/system/feature-flags-postgres" {
			return true
		}
		if slot == "ggg/identity" && adapter == "ggg/system/identity-dev" {
			return true
		}
		if slot == "ggg/llm" && adapter == "ggg/system/llm-fake" {
			return true
		}
		if slot == "ggg/mail" && adapter == "ggg/system/mail-dev" {
			return true
		}
		if slot == "ggg/notifications" && adapter == "ggg/system/notifications-postgres" {
			return true
		}
		if slot == "ggg/observability" && adapter == "ggg/system/observability-log" {
			return true
		}
		if slot == "ggg/rate-limit" && adapter == "ggg/system/rate-limit-memory" {
			return true
		}
		if slot == "ggg/realtime" && adapter == "ggg/system/realtime-postgres" {
			return true
		}
		if slot == "ggg/search" && adapter == "ggg/system/search-postgres" {
			return true
		}
		if slot == "ggg/storage" && adapter == "ggg/system/storage-filesystem" {
			return true
		}
		if slot == "ggg/telemetry" && adapter == "ggg/system/telemetry-noop" {
			return true
		}
		if slot == "ggg/usage" && adapter == "ggg/system/usage-postgres" {
			return true
		}
		if slot == "ggg/webhooks" && adapter == "ggg/system/webhooks-postgres" {
			return true
		}
	case "production":
		if slot == "ggg/analytics" && adapter == "ggg/system/analytics-noop" {
			return true
		}
		if slot == "ggg/audit-export" && adapter == "ggg/system/audit-export-otlp" {
			return true
		}
		if slot == "ggg/cache" && adapter == "ggg/system/cache-redis" {
			return true
		}
		if slot == "ggg/billing" && adapter == "ggg/system/billing-polar" {
			return true
		}
		if slot == "ggg/database" && adapter == "ggg/system/database-postgres" {
			return true
		}
		if slot == "ggg/feature-flags" && adapter == "ggg/system/feature-flags-postgres" {
			return true
		}
		if slot == "ggg/identity" && adapter == "ggg/system/identity-clerk" {
			return true
		}
		if slot == "ggg/llm" && adapter == "ggg/system/llm-openai-compatible" {
			return true
		}
		if slot == "ggg/mail" && adapter == "ggg/system/mail-resend" {
			return true
		}
		if slot == "ggg/notifications" && adapter == "ggg/system/notifications-postgres" {
			return true
		}
		if slot == "ggg/observability" && adapter == "ggg/system/observability-sentry" {
			return true
		}
		if slot == "ggg/rate-limit" && adapter == "ggg/system/rate-limit-redis" {
			return true
		}
		if slot == "ggg/realtime" && adapter == "ggg/system/realtime-ably" {
			return true
		}
		if slot == "ggg/search" && adapter == "ggg/system/search-postgres" {
			return true
		}
		if slot == "ggg/storage" && adapter == "ggg/system/storage-s3" {
			return true
		}
		if slot == "ggg/telemetry" && adapter == "ggg/system/telemetry-otlp" {
			return true
		}
		if slot == "ggg/usage" && adapter == "ggg/system/usage-postgres" {
			return true
		}
		if slot == "ggg/webhooks" && adapter == "ggg/system/webhooks-postgres" {
			return true
		}
	}
	return false
}
