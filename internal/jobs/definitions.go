// The core job declarations. Each returns a Definition wrapping a typed handler,
// so the generated dispatch table is pure data: it names these constructors and
// never knows a payload type.
//
// A module that adds a job adds one constructor here (or in its own file) and one
// JobContribution to its manifest; the table is regenerated from the manifest.
package jobs

import "context"

// defineEmailDigest is the per-user notification rollup. Schedulable: the
// schedule decides how often we look, the user's cadence decides who is due.
func (w *Worker) defineEmailDigest() Definition {
	return Define(KindEmailDigest, true, 0, w.sendDigests)
}

// defineUsageFlush pushes metered usage to the billing provider. Schedulable for
// the same reason, and a no-op when billing is unconfigured.
func (w *Worker) defineUsageFlush() Definition {
	return Define(KindUsageFlush, true, 0, w.flushUsage)
}

// defineWebhookDeliver needs its attempt state: it marks the delivery row
// permanently dead exactly when the job is about to be dead-lettered, so the
// customer-visible delivery status and the queue agree.
func (w *Worker) defineWebhookDeliver() Definition {
	return DefineWithAttempt(KindWebhookDeliver, false, 0, w.deliverWebhook)
}

func (w *Worker) defineExportProjectsCSV() Definition {
	return Define(KindExportProjectsCSV, false, 0, w.exportProjectsCSV)
}

func (w *Worker) defineExportOrgJSON() Definition {
	return Define(KindExportOrgJSON, false, 0, w.exportOrgJSON)
}

// The transactional emails share one body and differ only by kind, so each is a
// thin declaration over it. They are separate declarations rather than one
// multi-kind entry because a module owns a kind: billing owns dunning, onboarding
// owns the welcome mail, and removing one must not remove the others.
func (w *Worker) defineEmail(kind string) Definition {
	return Define(kind, false, 0, func(ctx context.Context, p EmailPayload) error {
		return w.sendTransactionalEmail(ctx, kind, p)
	})
}

func (w *Worker) defineEmailWelcome() Definition { return w.defineEmail(KindWelcome) }
func (w *Worker) defineEmailPaymentFailed() Definition {
	return w.defineEmail(KindPaymentFailed)
}
func (w *Worker) defineEmailSubscriptionCanceled() Definition {
	return w.defineEmail(KindSubscriptionCanceled)
}
func (w *Worker) defineEmailTrialEnding() Definition {
	return w.defineEmail(KindTrialEnding)
}
func (w *Worker) defineEmailDunningReminder() Definition {
	return w.defineEmail(KindDunningReminder)
}
func (w *Worker) defineEmailDunningFinal() Definition {
	return w.defineEmail(KindDunningFinal)
}
