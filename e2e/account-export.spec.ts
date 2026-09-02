import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

test.describe('account', () => {
  test('data export downloads JSON with the user data', async ({ browser }) => {
    const context = await loginAs(browser, 'pro');
    const page = await context.newPage();
    await page.goto('/app/settings/account');

    const downloadPromise = page.waitForEvent('download');
    await page.getByTestId('export-data').click();
    const download = await downloadPromise;
    expect(download.suggestedFilename()).toBe('gogogadget-data-export.json');

    const stream = await download.createReadStream();
    const chunks: Buffer[] = [];
    for await (const chunk of stream) chunks.push(chunk as Buffer);
    const body = JSON.parse(Buffer.concat(chunks).toString());
    expect(body.user.email).toBe('pro@gogogadget.dev');
    expect(body.memberships).toEqual(
      expect.arrayContaining([expect.objectContaining({ org_id: 'org_pro', role: 'org:admin' })]),
    );
    await context.close();
  });
});
