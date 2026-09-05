import type {
  FullConfig,
  Reporter,
  TestCase,
  TestResult,
} from '@playwright/test/reporter';

// Report every test that passed ONLY on retry, by name and attempt count.
//
// The suite runs with retries:2 in CI. A test that fails and then passes makes
// the run green, and the run's exit code is the whole of what the gate
// consumed — so a test failing a third of the time was indistinguishable from
// one that never failed, and export.spec.ts's storage bug sat behind that for
// an unknown number of runs before anyone measured it.
//
// Retries stay. The point is that the gate stops being silent about using
// them: a retried pass is reported, with the name and which attempt finally
// passed, and the absence of one is stated rather than left to inference. This
// does not fail the build — a retried pass is a signal to investigate, and
// turning it into a hard failure is how a suite gets its retries deleted.
class RetriedPassReporter implements Reporter {
  private retries = 0;
  private readonly passedOnRetry: { name: string; attempt: number; location: string }[] = [];

  onBegin(config: FullConfig): void {
    // Retries are per project; the budget the run had is the largest of them.
    this.retries = Math.max(0, ...config.projects.map((project) => project.retries));
  }

  onTestEnd(test: TestCase, result: TestResult): void {
    // onTestEnd fires once per attempt. The attempt that ends a retried test
    // is the one that reached its expected status with retry > 0.
    if (result.retry === 0 || result.status !== test.expectedStatus) return;
    // The file is already in the location, and the project matters (chromium
    // and mobile run different specs), so the name carries the project and
    // the titles and nothing else.
    const project = test.parent.project()?.name ?? '';
    const title = test
      .titlePath()
      .filter((segment) => segment && segment !== project && !segment.endsWith('.spec.ts'))
      .join(' › ');
    this.passedOnRetry.push({
      name: project ? `[${project}] ${title}` : title,
      attempt: result.retry + 1,
      location: `${test.location.file.split('/').pop()}:${test.location.line}`,
    });
  }

  onEnd(): void {
    if (this.retries === 0) {
      console.log('retried passes: none possible — this run had no retry budget');
      return;
    }
    if (this.passedOnRetry.length === 0) {
      console.log(`retried passes: none (retry budget ${this.retries})`);
      return;
    }
    const total = this.retries + 1;
    const count = this.passedOnRetry.length;
    console.log(
      `retried passes: ${count} ${count === 1 ? 'test' : 'tests'} failed at least once and was retried into a green run:`,
    );
    for (const item of this.passedOnRetry) {
      console.log(`  ${item.name} (${item.location}) — passed on attempt ${item.attempt} of ${total}`);
    }
  }

  printsToStdio(): boolean {
    return true;
  }
}

export default RetriedPassReporter;
