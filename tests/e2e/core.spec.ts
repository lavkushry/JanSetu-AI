import { test, expect, type Page, type Browser } from '@playwright/test';
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
