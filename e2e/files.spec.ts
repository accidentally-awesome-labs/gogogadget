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

  page.once('dialog', (d) => d.accept());
  await row.getByRole('button', { name: 'Delete' }).click();
  await expect(row).toBeHidden();

  await context.close();
});
