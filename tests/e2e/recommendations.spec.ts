import { test, expect } from '@playwright/test';

// Run against the opt-in isolated fixture harness in recommendation_browser_integration_test.go.
// No mutations are sent to the existing pilot environment by default.
test('recommendation consent, explanations, feedback, reset and following', async ({ page }) => {
  test.skip(
    process.env.JANSETU_RECOMMENDATION_BROWSER_PROOF !== '1',
    'Requires isolated recommendation fixture',
  );
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(e.message));
  await page.goto('/');
  const login = await page.request.post('/api/dev/session', {
    headers: { 'x-jansetu-csrf': '1' },
    data: { principalId: '10000000-0000-4000-8000-000000000001' },
  });
  expect(login.status()).toBe(200);
  const initial = await (await page.request.get('/api/me/recommendation-preferences')).json();
  if (initial.personalizationEnabled) {
    const reset = await page.request.put('/api/me/recommendation-preferences', {
      headers: { 'x-jansetu-csrf': '1', 'if-match': `"${initial.version}"` },
      data: { personalizationEnabled: false, interests: [], languages: [], locality: '' },
    });
    expect(reset.status()).toBe(200);
  }
  await page.reload();
  await page.getByRole('combobox', { name: 'Sort feed' }).selectOption('recommended');
  await expect(page.locator('.recommendation-control').first()).toBeVisible();
  await page
    .locator('.recommendation-control')
    .first()
    .getByText('Why this?', { exact: true })
    .click();
  await expect(page.getByRole('button', { name: 'More like this', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Helpful', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Not helpful', exact: true })).toHaveCount(0);
  await page.goto('/account');
  const settings = page.locator('#recommendation-settings');
  await expect(settings.getByRole('switch')).not.toBeChecked();
  await settings.getByRole('switch').check();
  await settings.getByRole('button', { name: 'Save recommendation preferences' }).click();
  await expect(settings.getByRole('switch')).toBeChecked();
  await expect
    .poll(
      async () =>
        (await (await page.request.get('/api/me/recommendation-preferences')).json())
          .personalizationEnabled,
    )
    .toBe(true);
  await page.goto('/');
  await page.getByRole('combobox', { name: 'Sort feed' }).selectOption('recommended');
  const control = page.locator('.recommendation-control:has(button)').first();
  await control.getByText('Why this?', { exact: true }).click();
  const eventResponse = page.waitForResponse(
    (r) => r.url().includes('/api/me/recommendation-events') && r.request().method() === 'POST',
  );
  await control.getByRole('button', { name: 'More like this', exact: true }).click();
  expect((await eventResponse).status()).toBe(200);
  await expect(control.getByRole('button', { name: 'More like this', exact: true })).toBeDisabled();
  // Save on the server but lose the acknowledgement; retry must reuse the event ID.
  const helpfulEvents: string[] = [];
  await page.route('**/api/me/recommendation-events', async (route) => {
    const body = route.request().postDataJSON();
    if (body.kind !== 'SATISFIED') {
      await route.continue();
      return;
    }
    helpfulEvents.push(body.eventId);
    const response = await route.fetch();
    if (helpfulEvents.length === 1) {
      expect(response.status()).toBe(200);
      await route.fulfill({
        status: 503,
        contentType: 'application/json',
        body: JSON.stringify({ message: 'Simulated lost acknowledgement' }),
      });
    } else {
      await route.fulfill({ response });
    }
  });
  const helpful = control.getByRole('button', { name: 'Helpful', exact: true });
  const lostResponse = page.waitForResponse(
    (r) => r.url().includes('/api/me/recommendation-events') && r.status() === 503,
  );
  await helpful.click();
  await lostResponse;
  await expect(helpful).toBeEnabled();
  await expect(helpful).toHaveAttribute('aria-pressed', 'false');
  await helpful.click();
  await expect(helpful).toHaveAttribute('aria-pressed', 'true');
  await expect(helpful).toBeDisabled();
  await expect(control.getByRole('button', { name: 'Not helpful', exact: true })).toBeDisabled();
  await expect(control.getByRole('button', { name: 'More like this', exact: true })).toBeDisabled();
  expect(helpfulEvents).toHaveLength(2);
  expect(helpfulEvents[0]).toBe(helpfulEvents[1]);
  await page.unroute('**/api/me/recommendation-events');
  const second = page.locator('.recommendation-control:has(button)').nth(1);
  await second.getByText('Why this?', { exact: true }).click();
  const unhelpfulResponse = page.waitForResponse(
    (r) =>
      r.url().includes('/api/me/recommendation-events') &&
      r.request().postDataJSON()?.kind === 'DISSATISFIED',
  );
  await second.getByRole('button', { name: 'Not helpful', exact: true }).click();
  expect((await unhelpfulResponse).status()).toBe(200);
  await expect(second.getByRole('button', { name: 'Not helpful', exact: true })).toHaveAttribute(
    'aria-pressed',
    'true',
  );
  await expect(second.getByRole('button', { name: 'More like this', exact: true })).toBeEnabled();
  await expect(second.getByRole('status')).toHaveText('Usefulness feedback recorded.');
  const snapshot = await (await page.request.get('/api/feed?sort=recommended')).json();
  expect(snapshot.nextCursor).toBeTruthy();
  await page.goto('/account');
  const summary = page.getByRole('region', { name: 'Your recommendation feedback' });
  await expect(summary.locator('dd')).toHaveText(['1', '0', '1', '1']);
  const before = await (await page.request.get('/api/me/recommendation-preferences')).json();
  await settings.getByRole('button', { name: 'Reset recommendation history' }).click();
  await expect
    .poll(
      async () =>
        (await (await page.request.get('/api/me/recommendation-preferences')).json()).generation,
    )
    .toBe(before.generation + 1);
  expect(
    (await page.request.get(`/api/feed?sort=recommended&cursor=${snapshot.nextCursor}`)).status(),
  ).toBe(410);
  await expect(summary.locator('dd')).toHaveText(['0', '0', '0', '0']);
  await settings.getByRole('switch').uncheck();
  await settings.getByRole('button', { name: 'Save recommendation preferences' }).click();
  await expect
    .poll(
      async () =>
        (await (await page.request.get('/api/me/recommendation-preferences')).json())
          .personalizationEnabled,
    )
    .toBe(false);
  await page.goto('/');
  await page.getByRole('combobox', { name: 'Sort feed' }).selectOption('recommended');
  await page.getByRole('button', { name: 'Following', exact: true }).click();
  await expect(page.getByRole('combobox', { name: 'Sort feed' })).toHaveValue('new');
  expect(errors).toEqual([]);
});
