import { test, expect, type Page, type Browser } from '@playwright/test';
import { readFileSync } from 'node:fs';
import type { components } from '../../apps/web/src/lib/generated';
type Schema = components['schemas'];
async function signIn(page: Page, name: string) {
  await page.getByRole('button', { name: 'Open account', exact: true }).click();
  const users: Record<string, string> = {
    'Ananya Rao': 'ananya',
    'Rohan Mehta': 'rohan',
    'Kiran Shah': 'coordinator',
    'City Works team': 'cityworks',
    'City Works': 'cityworks',
    'Neha Sen': 'verifier',
    'New neighbour': 'new-neighbour',
  };
  await page
    .getByRole('dialog')
    .getByRole('link', { name: /Continue to sign in|Sign in to another account/ })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Sign in to your account', exact: true }),
  ).toBeVisible();
  const restart = page.getByRole('button', { name: 'Restart login', exact: true });
  if (await restart.isVisible()) await restart.click();
  if (!users[name]) throw new Error('Unknown fixture account: ' + name);
  await page.getByRole('textbox', { name: /Username/ }).fill(users[name]);
  await page.getByLabel('Password', { exact: true }).fill('jansetu-demo');
  await page.getByRole('button', { name: 'Sign In', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Open account', exact: true })).toBeVisible();
  await expect.poll(async () => (await page.request.get('/api/me')).status()).toBe(200);
  await expect(page.locator('.account-control strong')).not.toHaveText('Explore JanSetu');
}
async function staffPage(browser: Browser, name: string) {
  const context = await browser.newContext();
  const page = await context.newPage();
  await page.goto('/');
  await signIn(page, name);
  await page.goto('/studio');
  return { page, context };
}
test('OIDC account provisioning, profile settings, and session revocation', async ({
  page,
  browser,
}) => {
  await page.goto('/');
  await signIn(page, 'New neighbour');
  await page.goto('/account');
  await expect(page.getByRole('heading', { name: 'Your public profile' })).toBeVisible();
  const me = (await (await page.request.get('/api/me')).json()) as Schema['Me'];
  expect(me.roles).toEqual([]);
  expect(me.agencies).toEqual([]);
  expect(JSON.stringify(me)).not.toContain('Private provider name');
  await expect(page.getByRole('link', { name: 'Staff workspace' })).toHaveCount(0);
  const handle = 'local_' + Date.now();
  await page.getByLabel('Display name', { exact: true }).fill('Local neighbour');
  await page.getByLabel('Handle', { exact: true }).fill(handle);
  await page.getByLabel('Bio', { exact: true }).fill('A public bio chosen in JanSetu.');
  await page.getByRole('button', { name: 'Save profile', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Your public profile is updated');
  await page.reload();
  await expect(page.getByLabel('Handle', { exact: true })).toHaveValue(handle);
  await expect(page.getByLabel('Bio', { exact: true })).toHaveValue(
    'A public bio chosen in JanSetu.',
  );
  const secondContext = await browser.newContext();
  const second = await secondContext.newPage();
  await second.goto('/');
  await signIn(second, 'New neighbour');
  await page.reload();
  await expect(page.getByText('Another session', { exact: true }).first()).toBeVisible();
  await page.getByRole('button', { name: 'Sign out other sessions', exact: true }).click();
  await expect.poll(async () => (await second.request.get('/api/me')).status()).toBe(401);
  await second.goto('/account');
  await expect(second.getByRole('heading', { name: 'Your account, your control' })).toBeVisible();
  expect((await page.request.get('/api/me')).status()).toBe(200);
  const sessions = await (await page.request.get('/api/me/sessions')).json();
  expect(sessions.items).toHaveLength(1);
  expect(sessions.items[0].current).toBe(true);
  await page.setViewportSize({ width: 320, height: 720 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: 'test-results/account-mobile.png' });
  await page.getByRole('button', { name: 'Sign out here', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Your account, your control' })).toBeVisible();
  expect((await page.request.get('/api/dev/accounts')).status()).toBe(404);
  await secondContext.close();
});
test('post review, votes, bookmarks, comments, edits, and deletion survive refresh', async ({
  page,
  browser,
}) => {
  const title = 'A neighbourhood reading circle ' + Date.now();
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  await page.getByRole('button', { name: 'Create a post', exact: true }).click();
  const composer = page.getByRole('dialog', { name: 'Start a conversation' });
  await composer
    .getByLabel('Community', { exact: true })
    .selectOption('50000000-0000-4000-8000-000000000001');
  await composer.getByLabel('Title', { exact: true }).fill(title);
  await composer
    .getByLabel('What’s on your mind?')
    .fill('Let us start a fictional weekly reading circle in our neighbourhood.');
  await composer.getByRole('button', { name: 'Submit for review', exact: true }).click();
  await page.waitForURL(/\/posts\//);
  await expect(
    page.getByText('Only you can see this post until a moderator approves it.'),
  ).toBeVisible();
  const postURL = page.url();
  const staff = await staffPage(browser, 'Kiran Shah');
  const review = staff.page.getByTestId('review-card').filter({ hasText: title });
  await review.getByLabel('Review reason').fill('Constructive fictional neighbourhood discussion');
  await review.getByRole('button', { name: 'Approve & publish' }).click();
  await expect(review).not.toBeVisible();
  await page.reload();
  await expect(page.getByRole('heading', { name: title, exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Upvote' })).toBeDisabled();
  await signIn(page, 'Rohan Mehta');
  await expect(page.getByRole('button', { name: 'Upvote' })).toBeEnabled();
  await page.getByRole('button', { name: 'Upvote' }).click();
  await expect(page.getByRole('button', { name: 'Upvote' })).toHaveAttribute(
    'aria-pressed',
    'true',
  );
  await page.getByRole('button', { name: 'Bookmark post' }).click();
  await expect(page.getByRole('button', { name: 'Remove bookmark' })).toBeVisible();
  await page.goto('/bookmarks');
  await expect(page.getByRole('heading', { name: title })).toBeVisible();
  await page.reload();
  await expect(page.getByRole('heading', { name: title })).toBeVisible();
  await page.goto(postURL);
  const comment = 'This fictional reading circle sounds useful ' + Date.now();
  await page.getByLabel('Add to the conversation').fill(comment);
  await page.getByRole('button', { name: 'Submit comment', exact: true }).click();
  await expect(page.getByText('Your comment is awaiting review.')).toBeVisible();
  await staff.page.reload();
  const commentReview = staff.page.getByTestId('review-card').filter({ hasText: comment });
  await commentReview
    .getByLabel('Review reason')
    .fill('Constructive comment relevant to this discussion');
  await commentReview.getByRole('button', { name: 'Approve & publish' }).click();
  await expect(commentReview).not.toBeVisible();
  await page.reload();
  await expect(page.getByText(comment, { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Upvote' }).click();
  await expect(page.getByRole('button', { name: 'Upvote' })).toHaveAttribute(
    'aria-pressed',
    'false',
  );
  await signIn(page, 'Ananya Rao');
  await page.getByLabel('Post options').click();
  await page.getByRole('button', { name: 'Edit post', exact: true }).click();
  const edit = page.getByRole('dialog', { name: 'Edit your post' });
  await edit
    .getByLabel('What’s on your mind?')
    .fill('A revised description is waiting for a moderator.');
  await edit.getByRole('button', { name: 'Submit for review', exact: true }).click();
  await expect(
    page.getByText('Your edit is awaiting review. The approved version remains public.'),
  ).toBeVisible();
  await expect(
    page.getByText('Let us start a fictional weekly reading circle in our neighbourhood.', {
      exact: true,
    }),
  ).toBeVisible();
  await page.getByLabel('Post options').click();
  await page.getByRole('button', { name: 'Delete post', exact: true }).click();
  await page
    .getByRole('dialog', { name: 'Delete this post?' })
    .getByRole('button', { name: 'Delete post' })
    .click();
  await expect(page.getByText('This post was deleted.')).toBeVisible();
  await page.reload();
  await expect(page.getByText('This post was deleted.')).toBeVisible();
  await staff.context.close();
});
test('private report, triage, agency work, independent verification, and reviewed public progress', async ({
  page,
  browser,
}) => {
  const statement = 'Fictional footpath damage for browser workflow ' + Date.now();
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  await page.getByRole('button', { name: 'Report an issue', exact: true }).first().click();
  const report = page.getByRole('dialog', { name: 'Report a service issue' });
  await report.getByLabel('Location or landmark').fill('Fictional crossing near 12th Main');
  await report.getByLabel('Describe the issue').fill(statement);
  await report.getByRole('button', { name: 'Review report', exact: true }).click();
  await report.getByLabel('I have reviewed this fictional report.').check();
  await report.getByRole('button', { name: 'Submit report', exact: true }).click();
  const ack = page.getByRole('dialog', { name: 'Your report was received' });
  await expect(
    ack.getByText('Agency acceptance has not been confirmed.', { exact: false }),
  ).toBeVisible();
  await ack.getByRole('link', { name: 'View my reports' }).click();
  await expect(page.getByText(statement, { exact: true })).toBeVisible();
  await page.reload();
  await expect(page.getByText(statement, { exact: true })).toBeVisible();
  const coord = await staffPage(browser, 'Kiran Shah');
  await coord.page.getByRole('button', { name: 'Service intake', exact: true }).click();
  const intake = coord.page.locator('.staff-card').filter({ hasText: statement });
  await intake.getByLabel('Assessed urgency').selectOption('2');
  await intake
    .getByLabel('Assessment reason')
    .fill('Synthetic accessibility impact needs a footpath restoration task');
  const triaged = coord.page.waitForResponse(
    (r) => r.url().endsWith('/triage') && r.request().method() === 'POST',
  );
  await intake.getByRole('button', { name: 'Create case & propose task' }).click();
  const caseId = (await (await triaged).json()).caseId as string;
  await expect(intake).not.toBeVisible();
  await coord.page.getByRole('button', { name: 'Service cases', exact: true }).click();
  await coord.page.getByTestId(`staff-case-${caseId}`).click();
  const officer = await staffPage(browser, 'City Works');
  await officer.page.getByTestId(`staff-case-${caseId}`).click();
  for (const label of ['Accept task', 'Start work', 'Claim completion']) {
    await officer.page
      .getByLabel('Work or decision summary')
      .fill('Fictional inspection and restoration activity recorded for this stage');
    await officer.page.getByRole('button', { name: label, exact: true }).click();
    if (label === 'Accept task')
      await expect(
        officer.page.getByRole('button', { name: 'Start work', exact: true }),
      ).toBeVisible();
    if (label === 'Start work')
      await expect(
        officer.page.getByRole('button', { name: 'Claim completion', exact: true }),
      ).toBeVisible();
  }
  await expect(
    officer.page.getByText('Completion is a claim until independently verified.'),
  ).toBeVisible();
  const verifier = await staffPage(browser, 'Neha Sen');
  await verifier.page.getByTestId(`staff-case-${caseId}`).click();
  await verifier.page
    .getByLabel('Independent inspection reason')
    .fill('Independent fictional site inspection confirms pedestrian access is restored');
  await verifier.page.getByRole('button', { name: 'Record verification decision' }).click();
  await expect(verifier.page.locator('.case-detail .receipt-eyebrow .badge')).toHaveText(
    'resolved',
  );
  await coord.page.reload();
  await coord.page.getByRole('button', { name: 'Service cases', exact: true }).click();
  await coord.page.getByTestId(`staff-case-${caseId}`).click();
  const safeTitle = 'Restored neighbourhood crossing ' + Date.now();
  await coord.page.getByLabel('Public title', { exact: true }).fill(safeTitle);
  await coord.page
    .getByLabel('Safe public summary')
    .fill('An independent reviewer verified that the fictional crossing is accessible again.');
  await coord.page.getByLabel('Broad public area').fill('Indiranagar');
  await coord.page.getByLabel('I reviewed this public preview for identifying details.').check();
  const published = coord.page.waitForResponse(
    (r) => r.url().endsWith('/publications') && r.request().method() === 'POST',
  );
  await coord.page.getByRole('button', { name: 'Publish reviewed progress' }).click();
  const receiptId = (await (await published).json()).receiptId as string;
  await page.goto(`/cases/${receiptId}`);
  await expect(page.getByRole('heading', { name: safeTitle })).toBeVisible();
  await expect(page.locator('.receipt-eyebrow .badge')).toHaveText('resolved');
  await expect(page.getByText(statement, { exact: true })).not.toBeVisible();
  await expect(
    page.getByText('An independent reviewer recorded restoration as verified.'),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Follow progress' }).click();
  await expect(page.getByRole('button', { name: 'Following', exact: true })).toBeVisible();
  await page.reload();
  await expect(page.getByRole('button', { name: 'Following', exact: true })).toBeVisible();
  await Promise.all([coord.context.close(), officer.context.close(), verifier.context.close()]);
});
test('responsive layouts, theme persistence, search, and community membership', async ({
  page,
}) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Your neighbourhood, connected.' })).toBeVisible();
  await page.screenshot({ path: 'test-results/desktop-light.png', fullPage: true });
  await page.getByRole('button', { name: 'Use dark theme' }).click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await page.screenshot({ path: 'test-results/desktop-dark.png', fullPage: true });
  await page.getByLabel('Search your city').fill('cycling');
  await page.getByLabel('Search your city').press('Enter');
  await expect(page.getByRole('heading', { name: 'Results for “cycling”' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Bengaluru Cycling', exact: true })).toBeVisible();
  await signIn(page, 'Rohan Mehta');
  await page.goto('/communities/50000000-0000-4000-8000-000000000002');
  await page.getByRole('button', { name: 'Joined', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Join community' })).toBeVisible();
  await page.getByRole('button', { name: 'Join community' }).click();
  await page
    .getByRole('dialog', { name: 'Community rules' })
    .getByRole('button', { name: 'Agree and join' })
    .click();
  await expect(page.getByRole('button', { name: 'Joined', exact: true })).toBeVisible();
  for (const width of [390, 320]) {
    await page.setViewportSize({ width, height: 844 });
    await page.goto('/');
    await expect(page.getByRole('navigation', { name: 'Mobile navigation' })).toBeVisible();
    await expect
      .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth))
      .toBe(true);
    await page.screenshot({ path: `test-results/mobile-dark-${width}.png`, fullPage: true });
  }
  await page.getByRole('button', { name: 'Use light theme' }).click();
  await page.getByRole('button', { name: 'Create post', exact: true }).click();
  await expect(page.getByRole('dialog', { name: 'Start a conversation' })).toBeVisible();
  await expect
    .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth))
    .toBe(true);
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).not.toBeVisible();
});
test('cross-origin mutations and another resident’s private report are denied', async ({
  page,
}) => {
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  const reports = await page.request.get('/api/my-reports');
  const own = (await reports.json()).items as Schema['ReportProgress'][];
  expect(own.length).toBeGreaterThan(0);
  await signIn(page, 'Rohan Mehta');
  expect((await page.request.get(`/api/my-reports/${own[0].id}`)).status()).toBe(404);
  expect(
    (
      await page.request.post('/api/me/logout', {
        headers: { origin: 'https://unrelated.example', 'x-jansetu-csrf': '1' },
      })
    ).status(),
  ).toBe(403);
  expect((await page.request.get('/api/me')).status()).toBe(200);
});

test('private photo upload, real OCR correction, draft restore, and report attachment', async ({
  page,
  browser,
}) => {
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  await page.getByRole('button', { name: 'Report an issue', exact: true }).first().click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Location or landmark').fill('Fictional OCR test crossing');
  await dialog.getByLabel('Describe the issue').fill('A fictional street light needs attention.');
  await dialog
    .getByLabel('Choose report photos')
    .setInputFiles('services/backend/internal/media/testdata/notice.png');
  await expect(
    dialog.getByRole('img', { name: 'Private report photo 1', exact: true }),
  ).toBeVisible();
  await dialog.getByRole('button', { name: 'Read text from photo', exact: true }).click();
  await expect(dialog.getByText('Review extracted text', { exact: true })).toBeVisible();
  // A text-equivalent word list is usable without a mouse or image overlay.
  await dialog.getByLabel('Word 1, original: BROKEN').fill('DAMAGED');
  await dialog
    .getByRole('button', { name: 'Add reviewed text to description', exact: true })
    .click();
  await expect(dialog.getByLabel('Describe the issue')).toHaveValue(/DAMAGED STREET LIGHT/);
  await dialog.getByLabel('Save this private draft on this device').check();
  const draft = await page.evaluate(
    () =>
      Object.entries(localStorage).find(([key]) => key.startsWith('jansetu.report-draft.'))?.[1],
  );
  expect(draft).toBeTruthy();
  expect(draft).not.toContain('token=');
  expect(draft).not.toContain('/api/media/');
  const mediaId = JSON.parse(draft!).mediaIds[0] as string;
  await page.setViewportSize({ width: 320, height: 720 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: 'test-results/private-ocr-mobile.png' });
  await dialog.getByRole('button', { name: 'Close dialog', exact: true }).click();
  await page.locator('main').getByRole('button', { name: 'Report an issue', exact: true }).click();
  await dialog.getByRole('button', { name: 'Restore draft', exact: true }).click();
  await expect(
    dialog.getByRole('img', { name: 'Private report photo 1', exact: true }),
  ).toBeVisible();
  await expect(dialog.getByLabel('Describe the issue')).toHaveValue(/DAMAGED STREET LIGHT/);
  await dialog.getByRole('button', { name: 'Review report', exact: true }).click();
  await dialog.getByLabel('I have reviewed this fictional report.').check();
  await dialog.getByRole('button', { name: 'Submit report', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Your report was received', exact: true }),
  ).toBeVisible();
  await page.getByRole('link', { name: 'View my reports', exact: true }).click();
  await expect(
    page.getByRole('img', { name: 'Private report evidence photo 1', exact: true }).first(),
  ).toBeVisible();
  expect((await page.request.get(`/api/media/${mediaId}/content`)).status()).toBe(200);
  const otherContext = await browser.newContext();
  const other = await otherContext.newPage();
  await other.goto('/');
  await signIn(other, 'Rohan Mehta');
  expect((await other.request.get(`/api/media/${mediaId}`)).status()).toBe(404);
  expect((await other.request.get(`/api/media/${mediaId}/content`)).status()).toBe(404);
  const publicFeed = JSON.stringify(await (await other.request.get('/api/feed')).json());
  expect(publicFeed).not.toContain(mediaId);
  expect(publicFeed).not.toContain('DAMAGED STREET LIGHT');
  await otherContext.close();
});

test('private object candidates have regions and preserve manual decisions', async ({
  page,
  browser,
}) => {
  await page.goto('/');
  await signIn(page, 'Rohan Mehta');
  await page.setViewportSize({ width: 320, height: 720 });
  await page.locator('main').getByRole('button', { name: 'Report an issue', exact: true }).click();
  const dialog = page.getByRole('dialog');
  const description = 'A fictional manual observation for an image recognition test.';
  await dialog.getByLabel('Describe the issue').fill(description);
  await dialog.getByLabel('Location or landmark').fill('Fictional image test court');
  await dialog.getByRole('combobox', { name: /Service category/ }).selectOption('OTHER');
  await dialog.getByLabel('Text language').selectOption('hi-IN');
  await dialog.getByLabel('Choose report photos').setInputFiles('tests/fixtures/vision-people.png');
  await expect(
    dialog.getByRole('img', { name: 'Private report photo 1', exact: true }),
  ).toBeVisible();
  await dialog.getByLabel('Include experimental object recognition').check();
  const analyzed = page.waitForResponse(
    (r) => r.url().endsWith('/analyses') && r.request().method() === 'POST',
  );
  await dialog.getByRole('button', { name: 'Analyze text and objects', exact: true }).click();
  const job = (await (await analyzed).json()) as Schema['Analysis'];
  const recognition = dialog.getByRole('region', { name: 'Object recognition review' });
  await expect(
    recognition.getByRole('listitem').filter({ hasText: 'Region 1: person' }),
  ).toBeVisible();
  await expect(
    dialog.getByText('OCR for this language is not enabled.', { exact: false }),
  ).toBeVisible();
  await expect(recognition.getByRole('img', { name: 'Object candidate regions' })).toBeVisible();
  expect(await recognition.locator('svg polygon').count()).toBeGreaterThan(0);
  await recognition.getByLabel('Show object regions').uncheck();
  await expect(recognition.locator('svg polygon')).toHaveCount(0);
  await expect(
    recognition.getByRole('listitem').filter({ hasText: 'Region 1: person' }),
  ).toBeVisible();
  await recognition.getByLabel('Show object regions').check();
  await expect(dialog.getByLabel('Describe the issue')).toHaveValue(description);
  await expect(dialog.getByRole('combobox', { name: /Service category/ })).toHaveValue('OTHER');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await recognition.getByRole('img', { name: 'Object candidate regions' }).scrollIntoViewIfNeeded();
  await page.screenshot({ path: 'test-results/private-recognition-mobile.png' });
  const result = (await (
    await page.request.get(`/api/analyses/${job.id}`)
  ).json()) as Schema['Analysis'];
  expect(result.state).toBe('PARTIAL');
  const task = result.tasks.find((t) => t.kind === 'ISSUE_DETECTION')!;
  expect(task.state).toBe('SUCCEEDED');
  expect(task.result!.modelVersion).toContain('sha256:');
  expect(task.result!.regions.every((r) => r.confidence === null)).toBe(true);
  const otherContext = await browser.newContext();
  const other = await otherContext.newPage();
  await other.goto('/');
  await signIn(other, 'Ananya Rao');
  expect((await other.request.get(`/api/analyses/${job.id}`)).status()).toBe(404);
  await otherContext.close();
  await dialog.getByRole('button', { name: 'Review report', exact: true }).click();
  await dialog.getByLabel('I have reviewed this fictional report.').check();
  await dialog.getByRole('button', { name: 'Submit report', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Your report was received', exact: true }),
  ).toBeVisible();
});

test('invalid photo removal and unsupported OCR preserve manual reporting', async ({ page }) => {
  await page.goto('/');
  await signIn(page, 'Rohan Mehta');
  await page.getByRole('button', { name: 'Report an issue', exact: true }).first().click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Choose report photos').setInputFiles({
    name: 'invalid.png',
    mimeType: 'image/png',
    buffer: Buffer.from('<svg>not an image</svg>'),
  });
  await expect(
    dialog.getByText(
      'This file could not be decoded as a supported photo. Choose another image or continue with text.',
    ),
  ).toBeVisible();
  await dialog.getByRole('button', { name: 'Remove photo', exact: true }).click();
  await expect(dialog.getByRole('article', { name: 'Photo 1' })).toHaveCount(0);
  await dialog.getByLabel('Text language').selectOption('hi-IN');
  await dialog
    .getByLabel('Choose report photos')
    .setInputFiles('services/backend/internal/media/testdata/notice.png');
  await expect(
    dialog.getByRole('img', { name: 'Private report photo 1', exact: true }),
  ).toBeVisible();
  await dialog.getByRole('button', { name: 'Read text from photo', exact: true }).click();
  await expect(
    dialog.getByText(
      'OCR for this language is not enabled. Your photo and typed description are still usable.',
    ),
  ).toBeVisible();
  await dialog.getByLabel('Location or landmark').fill('Fictional manual report crossing');
  await dialog
    .getByLabel('Describe the issue')
    .fill('Fictional issue typed manually while Hindi OCR is unavailable.');
  await dialog.getByRole('button', { name: 'Review report', exact: true }).click();
  await dialog.getByLabel('I have reviewed this fictional report.').check();
  await dialog.getByRole('button', { name: 'Submit report', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Your report was received', exact: true }),
  ).toBeVisible();
});

test('retry clears a lost upload-completion response without reuploading', async ({ page }) => {
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  await page.getByRole('button', { name: 'Report an issue', exact: true }).first().click();
  const dialog = page.getByRole('dialog');
  let completionCalls = 0;
  let partCalls = 0;
  page.on('request', (request) => {
    if (request.method() === 'PUT' && /\/api\/media\/[^/]+\/parts\/1/.test(request.url()))
      partCalls++;
  });
  await page.route('**/api/media/*/complete', async (route) => {
    completionCalls++;
    // The server commits completion; only its response to the browser is lost.
    const response = await route.fetch();
    expect(response.status()).toBe(202);
    await route.abort('failed');
  });
  await dialog
    .getByLabel('Choose report photos')
    .setInputFiles('services/backend/internal/media/testdata/notice.png');
  await expect(dialog.getByRole('button', { name: 'Retry upload', exact: true })).toBeVisible();
  await expect(
    dialog.getByRole('img', { name: 'Private report photo 1', exact: true }),
  ).toBeVisible();
  await dialog.getByRole('button', { name: 'Retry upload', exact: true }).click();
  await expect(dialog.getByRole('button', { name: 'Retry upload', exact: true })).toHaveCount(0);
  await expect(dialog.getByRole('article', { name: 'Photo 1' }).getByRole('alert')).toHaveCount(0);
  expect(completionCalls).toBe(1);
  expect(partCalls).toBe(1);
});

test('lost allocation response retries the same photo identity', async ({ page }) => {
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  await page.getByRole('button', { name: 'Report an issue', exact: true }).first().click();
  const dialog = page.getByRole('dialog');
  const allocations: Schema['UploadSession'][] = [];
  const identities: string[] = [];
  await page.route('**/api/media/uploads', async (route) => {
    identities.push(route.request().postDataJSON().clientUploadId);
    const response = await route.fetch();
    expect(response.status()).toBe(201);
    allocations.push(await response.json());
    if (allocations.length === 1) await route.abort('failed');
    else await route.fulfill({ response });
  });
  await dialog
    .getByLabel('Choose report photos')
    .setInputFiles('services/backend/internal/media/testdata/notice.png');
  await dialog.getByRole('button', { name: 'Retry adding photo', exact: true }).click();
  await expect(
    dialog.getByRole('img', { name: 'Private report photo 1', exact: true }),
  ).toBeVisible();
  expect(identities).toHaveLength(2);
  expect(identities[0]).toBe(identities[1]);
  expect(allocations[0].mediaId).toBe(allocations[1].mediaId);
  expect(allocations[0].uploadId).toBe(allocations[1].uploadId);
  await expect(dialog.getByRole('article', { name: 'Photo 1', exact: true })).toHaveCount(1);
  await expect(dialog.getByRole('button', { name: 'Retry adding photo', exact: true })).toHaveCount(
    0,
  );
  await dialog.getByRole('button', { name: 'Remove photo', exact: true }).click();
  await expect(dialog.getByRole('article', { name: 'Photo 1', exact: true })).toHaveCount(0);
});

test('multipart draft resumes after reload and rejects a different file before upload', async ({
  page,
}) => {
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  await page.getByRole('button', { name: 'Report an issue', exact: true }).first().click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Location or landmark').fill('Fictional resumed upload crossing');
  await dialog
    .getByLabel('Describe the issue')
    .fill('Fictional street light reported with a resumed private photo.');
  await dialog.getByLabel('Save this private draft on this device').check();
  const raw = Buffer.concat([
    readFileSync('services/backend/internal/media/testdata/notice.png'),
    Buffer.alloc(3 * 1024 * 1024),
  ]);
  let firstPartCalls = 0;
  let secondPartCalls = 0;
  page.on('request', (request) => {
    if (request.method() !== 'PUT') return;
    if (/\/parts\/1\?/.test(request.url())) firstPartCalls++;
    if (/\/parts\/2\?/.test(request.url())) secondPartCalls++;
  });
  await page.route('**/api/media/*/parts/2?*', async (route) => {
    if (secondPartCalls === 1) await route.abort('failed');
    else await route.continue();
  });
  const file = { name: 'resume-test.png', mimeType: 'image/png', buffer: raw };
  await dialog.getByLabel('Choose report photos').setInputFiles(file);
  await expect(dialog.getByRole('button', { name: 'Retry upload', exact: true })).toBeVisible();
  await expect(dialog.getByText('1 of 2 parts uploaded', { exact: true })).toBeVisible();
  const stored = await page.evaluate(
    () => Object.entries(localStorage).find(([k]) => k.startsWith('jansetu.report-draft.'))?.[1],
  );
  expect(stored).toBeTruthy();
  for (const secret of ['token=', '/api/media/', 'resume-test.png', 'sourceSha256', 'base64'])
    expect(stored).not.toContain(secret);
  const mediaId = JSON.parse(stored!).mediaIds[0];
  const status = await (await page.request.get(`/api/media/${mediaId}/upload`)).json();
  expect(status.completedParts).toHaveLength(1);
  expect(status.parts).toHaveLength(0);
  await page.reload();
  await page.getByRole('button', { name: 'Report an issue', exact: true }).first().click();
  await dialog.getByRole('button', { name: 'Restore draft', exact: true }).click();
  await expect(
    dialog.getByRole('button', { name: 'Choose same photo to resume', exact: true }),
  ).toBeVisible();
  await page.setViewportSize({ width: 320, height: 720 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await expect(
    dialog.getByRole('progressbar', { name: 'Photo 1 upload progress' }),
  ).toHaveAttribute('value', '1');
  await dialog.getByRole('article', { name: 'Photo 1', exact: true }).scrollIntoViewIfNeeded();
  await page.screenshot({ path: 'test-results/upload-recovery-mobile.png' });
  const wrong = Buffer.from(raw);
  wrong[100] ^= 1;
  await dialog
    .getByLabel('Resume photo 1', { exact: true })
    .setInputFiles({ ...file, buffer: wrong });
  await expect(
    dialog.getByText('This is a different photo. Choose the original photo to resume.', {
      exact: true,
    }),
  ).toBeVisible();
  expect(firstPartCalls).toBe(1);
  expect(secondPartCalls).toBe(1);
  await dialog.getByLabel('Resume photo 1', { exact: true }).setInputFiles(file);
  await expect(
    dialog.getByRole('img', { name: 'Private report photo 1', exact: true }),
  ).toBeVisible();
  await expect(dialog.getByRole('article', { name: 'Photo 1' }).getByRole('alert')).toHaveCount(0);
  expect(firstPartCalls).toBe(1);
  expect(secondPartCalls).toBe(2);
  await page.setViewportSize({ width: 320, height: 720 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: 'test-results/resumed-photo-mobile.png' });
  await dialog.getByRole('button', { name: 'Read text from photo', exact: true }).click();
  await expect(dialog.getByLabel('Word 1, original: BROKEN')).toBeVisible();
  await dialog.getByRole('button', { name: 'Review report', exact: true }).click();
  await dialog.getByLabel('I have reviewed this fictional report.').check();
  await dialog.getByRole('button', { name: 'Submit report', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Your report was received', exact: true }),
  ).toBeVisible();
});

test('a committed part with a lost acknowledgement completes after reload without a file', async ({
  page,
}) => {
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  await page.getByRole('button', { name: 'Report an issue', exact: true }).first().click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Save this private draft on this device').check();
  let partCalls = 0;
  await page.route('**/api/media/*/parts/1?*', async (route) => {
    partCalls++;
    const response = await route.fetch();
    expect(response.status()).toBe(200);
    await route.abort('failed');
  });
  await dialog
    .getByLabel('Choose report photos')
    .setInputFiles('services/backend/internal/media/testdata/notice.png');
  await expect(dialog.getByRole('button', { name: 'Retry upload', exact: true })).toBeVisible();
  await page.reload();
  await page.getByRole('button', { name: 'Report an issue', exact: true }).first().click();
  await dialog.getByRole('button', { name: 'Restore draft', exact: true }).click();
  await dialog.getByRole('button', { name: 'Finish upload', exact: true }).click();
  await expect(
    dialog.getByRole('img', { name: 'Private report photo 1', exact: true }),
  ).toBeVisible();
  expect(partCalls).toBe(1);
  await expect(dialog.getByRole('article', { name: 'Photo 1' }).getByRole('alert')).toHaveCount(0);
  await dialog.getByRole('button', { name: 'Remove photo', exact: true }).click();
  await expect(dialog.getByRole('article', { name: 'Photo 1' })).toHaveCount(0);
  await dialog.getByLabel('Save this private draft on this device').uncheck();
});
