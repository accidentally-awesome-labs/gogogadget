import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

// Files: upload (DevStore) → listed → download bytes → delete.
test('upload, download, delete a file', async ({ browser }) => {
  const context = await loginAs(browser, 'pro');
  const page = await context.newPage();
  await page.goto('/app/files');
  await expect(page.getByRole('heading', { name: 'Files', exact: true })).toBeVisible();
  await expect(page.getByTestId('storage-meter')).toContainText('5000');

  await page.getByTestId('file-input').setInputFiles({
    name: 'e2e-note.txt',
    mimeType: 'text/plain',
    buffer: Buffer.from('e2e upload payload'),
  });
  await page.getByRole('button', { name: 'Upload' }).click();

  const row = page.getByTestId('file-row').filter({ hasText: 'e2e-note.txt' });
  await expect(row).toBeVisible();

  // Download is an attachment with the same bytes.
  const downloadPromise = page.waitForEvent('download');
  await row.getByRole('link', { name: 'Download' }).click();
  const download = await downloadPromise;
  const stream = await download.createReadStream();
  const chunks: Buffer[] = [];
  for await (const chunk of stream) chunks.push(chunk as Buffer);
  expect(Buffer.concat(chunks).toString()).toBe('e2e upload payload');

  // Delete is gated by an in-page AlertDialog, not window.confirm. No
  // page.on('dialog') handler on purpose: a regression that brings the native
  // prompt back must hang this test, not pass it. [open] scopes the locator to
  // the dialog actually on screen — there is one per row.
  const openDialog = page.locator('dialog[data-ui="alert-dialog"][open]');
  await row.getByRole('button', { name: 'Delete' }).click();
  await expect(openDialog).toBeVisible();
  // Not deleted yet: that is what makes it a confirmation.
  await expect(row).toBeVisible();

  await openDialog.getByRole('button', { name: 'Delete permanently' }).click();
  await expect(row).toBeHidden();

  await context.close();
});
