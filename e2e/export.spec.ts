import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

// CSV export dogfoods jobs + storage + notifications: click → poll /app/files
// until the export row appears (the dev server runs the worker) → download
// returns CSV bytes.
test('export produces a downloadable CSV', async ({ browser }) => {
  const context = await loginAs(browser, 'pro');
  const page = await context.newPage();
  await page.goto('/app/projects');

  await page.getByTestId('projects-export').click();
  await expect(page.getByTestId('toast').first()).toContainText('Export started');

  // The worker completes async — poll the files page until the export row lands.
  await expect(async () => {
    await page.goto('/app/files');
    await expect(page.getByTestId('file-row').filter({ hasText: 'projects-' }).first()).toBeVisible();
  }).toPass({ timeout: 20000, intervals: [1000, 2000] });

  // Download returns CSV bytes.
  const downloadPromise = page.waitForEvent('download');
  await page.getByTestId('file-row').filter({ hasText: 'projects-' }).first().getByRole('link', { name: 'Download' }).click();
  const download = await downloadPromise;
  const stream = await download.createReadStream();
  const chunks: Buffer[] = [];
  for await (const chunk of stream) chunks.push(chunk as Buffer);
  expect(Buffer.concat(chunks).toString()).toContain('id,name,status,created_at');

  await context.close();
});
