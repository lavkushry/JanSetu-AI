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
async function publicProgressFixture(owner: Page, publisher: Page) {
  const csrf = { 'x-jansetu-csrf': '1' };
  const statement = `Private lifecycle report ${Date.now()} ${crypto.randomUUID()}`;
  const response = await owner.request.post('/api/service-reports', {
    headers: { ...csrf, 'idempotency-key': crypto.randomUUID() },
    data: {
      clientSubmissionId: crypto.randomUUID(),
      statement,
      languageTag: 'en-IN',
      category: 'FOOTPATH',
      locationLabel: 'Fictional lifecycle crossing',
      publicationPreference: 'SANITIZED_RECEIPT',
    },
  });
  expect(response.status()).toBe(201);
  const report = (await response.json()) as { id: string; receivedAt: string };
  const triage = await publisher.request.post(`/api/authority/reports/${report.id}/triage`, {
    headers: { ...csrf, 'if-match': '"1"' },
    data: {
      agencyId: '30000000-0000-4000-8000-000000000001',
      category: 'FOOTPATH',
      urgencyTier: 2,
      reason: 'Fictional lifecycle restoration assessment',
    },
  });
  expect(triage.status()).toBe(201);
  const { caseId } = (await triage.json()) as { caseId: string };
  return { caseId, report, statement };
}
async function openPublicationReview(publisher: Page, caseId: string) {
  await publisher.goto('/studio');
  await publisher.getByRole('button', { name: 'Service cases', exact: true }).click();
  await publisher.getByTestId(`staff-case-${caseId}`).click();
  await expect(publisher.getByTestId('publication-review')).toBeVisible();
}
async function savePublicProgress(publisher: Page, button: string) {
  const response = publisher.waitForResponse(
    (r) => r.url().endsWith('/publications') && r.request().method() === 'POST',
  );
  await publisher.getByRole('button', { name: button, exact: true }).click();
  const saved = await response;
  expect(saved.status()).toBe(200);
  return (await saved.json()) as Schema['PublicationResult'];
}

test('publisher corrections, withdrawal and reviewed republication preserve private case work', async ({
  page,
  browser,
}) => {
  test.setTimeout(120_000);
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  const staff = await staffPage(browser, 'Kiran Shah');
  const follower = await staffPage(browser, 'Rohan Mehta');
  const officer = await staffPage(browser, 'City Works team');
  try {
    const { caseId, report, statement } = await publicProgressFixture(page, staff.page);
    await openPublicationReview(staff.page, caseId);
    const review = staff.page.getByTestId('publication-review');
    const current = review.getByTestId('current-publication');
    const title = `Reviewed lifecycle crossing ${Date.now()}`;
    await review.getByLabel('Public title', { exact: true }).fill(title);
    await review
      .getByLabel('Safe public summary')
      .fill('The fictional crossing has a proposed restoration task.');
    await review.getByLabel('Broad public area').fill('Synthetic neighbourhood');
    await review
      .getByRole('textbox', { name: 'Private publication reason', exact: true })
      .fill('PRIVATE FIRST PUBLICATION REASON');
    await review.getByLabel('I reviewed this public preview for identifying details.').check();
    const first = await savePublicProgress(staff.page, 'Publish reviewed progress');
    expect(first.version).toBe(1);
    expect(first.publicationVersion).toBe(1);
    const receiptId = first.receiptId;
    await expect(current).toContainText('PUBLICATION REVISION 1');
    await follower.page.goto(`/cases/${receiptId}`);
    await follower.page.getByRole('button', { name: 'Follow progress', exact: true }).click();
    await expect(
      follower.page.getByRole('button', { name: 'Following', exact: true }),
    ).toBeVisible();
    const activity = async () =>
      (await (
        await follower.page.request.get('/api/me/activity?filter=CASES')
      ).json()) as Schema['ActivityPage'];
    const correctedTitle = `Corrected lifecycle crossing ${Date.now()}`;
    await review.getByLabel('Public title', { exact: true }).fill(correctedTitle);
    await review
      .getByRole('textbox', { name: 'Private publication reason', exact: true })
      .fill('PRIVATE CORRECTION REASON');
    await review.getByLabel('I reviewed this public preview for identifying details.').check();
    // Hold the case refresh after a committed correction: the new reviewed
    // base must not announce a conflict while the query still has the old data.
    let releaseRefresh = () => {};
    const refreshGate = new Promise<void>((resolve) => {
      releaseRefresh = resolve;
    });
    let refreshingCase = false;
    await staff.page.route(
      `**/api/authority/cases/${caseId}`,
      async (route) => {
        const response = await route.fetch();
        refreshingCase = true;
        await refreshGate;
        await route.fulfill({ response });
      },
      { times: 1 },
    );
    try {
      const correction = await savePublicProgress(staff.page, 'Save reviewed correction');
      expect(correction.version).toBe(1);
      expect(correction.publicationVersion).toBe(2);
      await expect.poll(() => refreshingCase).toBe(true);
      await expect(current).toContainText('PUBLICATION REVISION 1');
      await expect(review.getByRole('alert')).toHaveCount(0);
    } finally {
      releaseRefresh();
    }
    await expect(current).toContainText('PUBLICATION REVISION 2');
    await expect
      .poll(async () => (await activity()).items.filter((n) => n.target.id === receiptId).length)
      .toBe(1);
    const old = (await activity()).items.find((n) => n.target.id === receiptId);
    if (!old) throw new Error('Missing reviewed progress notice');
    await follower.page.goto('/activity');
    await follower.page.getByRole('button', { name: 'Service progress', exact: true }).click();
    const card = follower.page
      .getByTestId('activity-card')
      .filter({ has: follower.page.locator(`a[href="/cases/${receiptId}"]`) });
    await expect(card).toContainText(correctedTitle);
    await card.getByRole('button', { name: 'Mark as read', exact: true }).click();
    await expect(card).not.toHaveClass(/unread/);
    await staff.page.setViewportSize({ width: 320, height: 820 });
    await review.getByRole('button', { name: 'Withdraw public progress', exact: true }).click();
    const dialog = staff.page.getByRole('dialog', { name: 'Withdraw public progress?' });
    await dialog.getByLabel('Private withdrawal reason').fill('PRIVATE WITHDRAWAL REVIEW REASON');
    await dialog.getByLabel('I reviewed the withdrawal of this public progress.').check();
    expect(
      await staff.page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
    ).toBe(true);
    await staff.page.screenshot({
      path: 'test-results/public-progress-withdrawal-mobile-light.png',
    });
    await dialog.getByRole('button', { name: 'Keep public progress', exact: true }).click();
    expect((await follower.page.request.get(`/api/case-receipts/${receiptId}`)).status()).toBe(200);
    await staff.page.getByRole('button', { name: 'Use dark theme', exact: true }).click();
    await review.getByRole('button', { name: 'Withdraw public progress', exact: true }).click();
    await expect(dialog.getByLabel('Private withdrawal reason')).toHaveValue(
      'PRIVATE WITHDRAWAL REVIEW REASON',
    );
    await expect(
      dialog.getByLabel('I reviewed the withdrawal of this public progress.'),
    ).not.toBeChecked();
    await dialog.getByLabel('I reviewed the withdrawal of this public progress.').check();
    expect(
      await staff.page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
    ).toBe(true);
    await staff.page.screenshot({
      path: 'test-results/public-progress-withdrawal-mobile-dark.png',
    });
    await dialog.getByRole('button', { name: 'Confirm withdrawal', exact: true }).click();
    await expect(dialog).toHaveCount(0);
    await expect(current).toContainText('PUBLICATION REVISION 3');
    await expect(current.getByText('withdrawn', { exact: true })).toBeVisible();
    await expect(
      review.getByRole('button', { name: 'Republish reviewed progress', exact: true }),
    ).toBeDisabled();
    await follower.page.reload();
    await follower.page.getByRole('button', { name: 'Service progress', exact: true }).click();
    await expect(card).toHaveCount(0);
    expect((await activity()).items.some((n) => n.target.id === receiptId)).toBe(false);
    expect((await follower.page.request.get(`/api/case-receipts/${receiptId}`)).status()).toBe(404);
    await follower.page.goto(`/cases/${receiptId}`);
    await expect(follower.page.locator('main').getByRole('alert')).toContainText('unavailable');
    await expect(follower.page.locator('main')).not.toContainText(correctedTitle);
    await page.goto('/my-reports');
    const own = page.locator('.my-report').filter({ hasText: statement });
    await expect(own).toBeVisible();
    await expect(own.getByRole('link', { name: /View reviewed public progress/ })).toHaveCount(0);
    await officer.page.goto('/studio');
    await officer.page.getByTestId(`staff-case-${caseId}`).click();
    await expect(officer.page.getByTestId('publication-review')).toHaveCount(0);
    await officer.page
      .getByLabel('Work or decision summary')
      .fill('PRIVATE AGENCY WORK AFTER PUBLIC WITHDRAWAL');
    await officer.page.getByRole('button', { name: 'Accept task', exact: true }).click();
    await expect(
      officer.page.getByRole('button', { name: 'Start work', exact: true }),
    ).toBeVisible();
    const refreshedReview = staff.page.waitForResponse(
      (r) => r.url().endsWith(`/authority/cases/${caseId}`) && r.request().method() === 'GET',
    );
    const refreshButton = review.getByRole('button', {
      name: 'Refresh publication review',
      exact: true,
    });
    await refreshButton.click();
    expect((await refreshedReview).status()).toBe(200);
    // Refresh clears the acknowledgement; wait for its state update before editing.
    await expect(refreshButton).toBeEnabled();
    const freshTitle = `Fresh reviewed lifecycle crossing ${Date.now()}`;
    await review.getByLabel('Public title', { exact: true }).fill(freshTitle);
    await review
      .getByLabel('Safe public summary')
      .fill('Agency acceptance was reviewed for fresh public progress.');
    await review
      .getByRole('textbox', { name: 'Private publication reason', exact: true })
      .fill('PRIVATE FRESH REPUBLICATION REASON');
    await review.getByLabel('I reviewed this public preview for identifying details.').check();
    const fresh = await savePublicProgress(staff.page, 'Republish reviewed progress');
    expect(fresh.receiptId).toBe(receiptId);
    expect(fresh.version).toBe(2);
    expect(fresh.publicationVersion).toBe(4);
    await expect
      .poll(async () => (await activity()).items.filter((n) => n.target.id === receiptId).length)
      .toBe(1);
    const publicResponse = await follower.page.request.get(`/api/case-receipts/${receiptId}`);
    expect(publicResponse.status()).toBe(200);
    const publicReceipt = (await publicResponse.json()) as Schema['Receipt'];
    expect(publicReceipt.title).toBe(freshTitle);
    expect(publicReceipt.version).toBe(4);
    expect(publicReceipt.firstReportedAt).toBe(report.receivedAt);
    for (const secret of ['PRIVATE', caseId, report.id, statement])
      expect(JSON.stringify(publicReceipt)).not.toContain(secret);
    await follower.page.goto('/activity');
    await follower.page.getByRole('button', { name: 'Service progress', exact: true }).click();
    await expect(card).toContainText(freshTitle);
    await expect(card).toHaveClass(/unread/);
    expect(
      (
        await follower.page.request.put(`/api/me/activity/${old.id}/read`, {
          headers: { 'x-jansetu-csrf': '1' },
          data: { read: false },
        })
      ).status(),
    ).toBe(404);
    const detail = (await (
      await staff.page.request.get(`/api/authority/cases/${caseId}`)
    ).json()) as Schema['CaseDetail'];
    expect(detail.version).toBe(2);
    expect(detail.firstReportedAt).toBe(report.receivedAt);
    expect(detail.publication?.decisions.map((d) => d.action)).toEqual([
      'PUBLISH',
      'WITHDRAW',
      'CORRECT',
      'PUBLISH',
    ]);
  } finally {
    await Promise.all([staff.context.close(), follower.context.close(), officer.context.close()]);
  }
});

test('publication review conflicts preserve drafts and reconcile a lost response', async ({
  page,
  browser,
}) => {
  test.setTimeout(120_000);
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  const staff = await staffPage(browser, 'Kiran Shah');
  try {
    const { caseId, report } = await publicProgressFixture(page, staff.page);
    const title = `Publication recovery crossing ${Date.now()}`;
    const fields = {
      title,
      summary: 'The fictional crossing awaits reviewed restoration work.',
      area: 'Synthetic neighbourhood',
      reason: 'PRIVATE RECOVERY INITIAL REVIEW',
      reviewed: true,
      publicationVersion: 0,
    };
    const publish = (data: typeof fields) =>
      staff.page.request.post(`/api/authority/cases/${caseId}/publications`, {
        headers: { 'x-jansetu-csrf': '1', 'if-match': '"1"' },
        data,
      });
    const initial = await publish(fields);
    expect(initial.status()).toBe(200);
    const { receiptId } = (await initial.json()) as Schema['PublicationResult'];
    await openPublicationReview(staff.page, caseId);
    const review = staff.page.getByTestId('publication-review');
    const current = review.getByTestId('current-publication');
    const confirmation = review.getByLabel(
      'I reviewed this public preview for identifying details.',
    );
    const draft = `Local correction draft ${Date.now()}`;
    await review.getByLabel('Public title', { exact: true }).fill(draft);
    await review
      .getByRole('textbox', { name: 'Private publication reason', exact: true })
      .fill('PRIVATE LOCAL DRAFT REASON');
    await confirmation.check();
    const concurrent = await publish({
      ...fields,
      title: 'Concurrent reviewed public correction',
      publicationVersion: 1,
    });
    expect(concurrent.status()).toBe(200);
    const staleResponse = staff.page.waitForResponse(
      (r) => r.url().endsWith('/publications') && r.request().method() === 'POST',
    );
    await review.getByRole('button', { name: 'Save reviewed correction', exact: true }).click();
    expect((await staleResponse).status()).toBe(409);
    await expect(review.getByRole('alert').filter({ hasText: 'Your draft is kept' })).toBeVisible();
    await expect(review.getByLabel('Public title', { exact: true })).toHaveValue(draft);
    await expect(
      review.getByRole('textbox', { name: 'Private publication reason', exact: true }),
    ).toHaveValue('PRIVATE LOCAL DRAFT REASON');
    await expect(
      review.getByRole('button', { name: 'Save reviewed correction', exact: true }),
    ).toBeDisabled();
    await review.getByRole('button', { name: 'Refresh publication review', exact: true }).click();
    await expect(current).toContainText('PUBLICATION REVISION 2');
    await expect(current).toContainText('Concurrent reviewed public correction');
    await expect(review.getByLabel('Public title', { exact: true })).toHaveValue(draft);
    await expect(confirmation).not.toBeChecked();
    await confirmation.check();
    expect(
      (await savePublicProgress(staff.page, 'Save reviewed correction')).publicationVersion,
    ).toBe(3);
    await expect(current).toContainText('PUBLICATION REVISION 3');
    const lostTitle = `Committed response lost crossing ${Date.now()}`;
    await review.getByLabel('Public title', { exact: true }).fill(lostTitle);
    await review
      .getByRole('textbox', { name: 'Private publication reason', exact: true })
      .fill('PRIVATE LOST RESPONSE REVIEW');
    await confirmation.check();
    await staff.page.route(
      `**/api/authority/cases/${caseId}/publications`,
      async (route) => {
        const committed = await route.fetch();
        expect(committed.status()).toBe(200);
        await route.abort('failed');
      },
      { times: 1 },
    );
    await review.getByRole('button', { name: 'Save reviewed correction', exact: true }).click();
    await expect(review.getByRole('alert')).toBeVisible();
    const actual = (await (
      await staff.page.request.get(`/api/case-receipts/${receiptId}`)
    ).json()) as Schema['Receipt'];
    expect(actual.title).toBe(lostTitle);
    expect(actual.version).toBe(4);
    const retryResponse = staff.page.waitForResponse(
      (r) => r.url().endsWith('/publications') && r.request().method() === 'POST',
    );
    await review.getByRole('button', { name: 'Save reviewed correction', exact: true }).click();
    expect((await retryResponse).status()).toBe(409);
    await review.getByRole('button', { name: 'Refresh publication review', exact: true }).click();
    await expect(current).toContainText('PUBLICATION REVISION 4');
    await expect(review.getByLabel('Public title', { exact: true })).toHaveValue(lostTitle);
    await expect(
      review.getByRole('textbox', { name: 'Private publication reason', exact: true }),
    ).toHaveValue('PRIVATE LOST RESPONSE REVIEW');
    await expect(confirmation).not.toBeChecked();
    await confirmation.check();
    const unchanged = await savePublicProgress(staff.page, 'Save reviewed correction');
    expect(unchanged.publicationVersion).toBe(4);
    expect(unchanged.version).toBe(1);
    const detail = (await (
      await staff.page.request.get(`/api/authority/cases/${caseId}`)
    ).json()) as Schema['CaseDetail'];
    expect(detail.publication?.decisions).toHaveLength(4);
    expect(detail.firstReportedAt).toBe(report.receivedAt);
    expect(detail.version).toBe(1);
  } finally {
    await staff.context.close();
  }
});

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
  const communityId = '50000000-0000-4000-8000-000000000001';
  const community = (await (
    await page.request.get(`/api/communities/${communityId}`)
  ).json()) as Schema['Community'];
  expect(
    (
      await page.request.put(`/api/communities/${communityId}/membership`, {
        headers: { 'x-jansetu-csrf': '1' },
        data: { joined: true, rulesRevision: community.rulesRevision },
      })
    ).status(),
  ).toBe(200);
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

test('conversation pages, reply context and collapse controls survive refresh', async ({
  page,
  browser,
}) => {
  test.setTimeout(90_000);
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  const staff = await staffPage(browser, 'Kiran Shah');
  const other = await browser.newContext();
  const neighbour = await other.newPage();
  await neighbour.goto('/');
  await signIn(neighbour, 'Rohan Mehta');
  async function approve(target: string) {
    const queue = (await (await staff.page.request.get('/api/moderation')).json()) as {
      items: Schema['Review'][];
    };
    const item = queue.items.find((row) => row.postId === target || row.commentId === target);
    expect(item).toBeDefined();
    if (!item) throw new Error('Missing fixture review');
    const response = await staff.page.request.post(`/api/moderation/${item.id}/decisions`, {
      headers: { 'x-jansetu-csrf': '1', 'if-match': `"${item.version}"` },
      data: {
        action: 'ALLOW',
        reason: 'Constructive fictional pagination fixture',
        targetRevision: item.targetRevision,
      },
    });
    expect(response.status()).toBe(200);
  }
  const response = await page.request.post('/api/posts', {
    headers: { 'x-jansetu-csrf': '1', 'idempotency-key': crypto.randomUUID() },
    data: {
      kind: 'SHORT',
      body: `Fictional paginated conversation ${Date.now()}`,
      languageTag: 'en-IN',
      mediaIds: [],
      submitForReview: true,
    },
  });
  expect(response.status()).toBe(201);
  const post = (await response.json()) as Schema['Post'];
  await approve(post.id);
  async function comment(writer: Page, body: string, parentId: string | null = null) {
    const response = await writer.request.post(`/api/posts/${post.id}/comments`, {
      headers: { 'x-jansetu-csrf': '1', 'idempotency-key': crypto.randomUUID() },
      data: { body, languageTag: 'en-IN', parentId },
    });
    expect(response.status()).toBe(201);
    const result = (await response.json()) as { id: string };
    await approve(result.id);
    return result.id;
  }
  const rootBody = `A constructive root comment ${Date.now()}`;
  const rootId = await comment(page, rootBody);
  for (let i = 0; i < 19; i++) await comment(page, `Fictional top-level conversation entry ${i}`);
  const childBody = `A reply with stable parent context ${Date.now()}`;
  const childId = await comment(neighbour, childBody, rootId);
  const grandBody = `A nested constructive reply ${Date.now()}`;
  const grandId = await comment(page, grandBody, childId);
  let lateParent = '';
  for (let i = 0; i < 3; i++) {
    const id = await comment(i === 2 ? neighbour : page, `Fictional later conversation entry ${i}`);
    if (i === 2) lateParent = id;
  }
  await page.goto(`/posts/${post.id}`);
  await expect(page.locator('article.comment')).toHaveCount(20);
  await expect(page.getByText(childBody, { exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: 'Load more comments', exact: true }).click();
  await expect(page.locator('article.comment')).toHaveCount(25);
  await expect(page.getByRole('button', { name: 'Load more comments', exact: true })).toHaveCount(
    0,
  );
  const root = page.getByTestId(`comment-${rootId}`);
  const child = page.getByTestId(`comment-${childId}`);
  const grandchild = page.getByTestId(`comment-${grandId}`);
  await child.getByRole('link', { name: 'Reply to Ananya Rao', exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`#comment-${rootId}$`));
  const collapse = root.getByRole('button', { name: 'Hide replies (2 loaded)', exact: true });
  await collapse.focus();
  await collapse.press('Enter');
  await expect(child).not.toBeVisible();
  await expect(grandchild).not.toBeVisible();
  await expect(
    root.getByRole('button', { name: 'Show replies (2 loaded)', exact: true }),
  ).toHaveAttribute('aria-expanded', 'false');
  await root.getByRole('button', { name: 'Show replies (2 loaded)', exact: true }).click();
  await child.getByRole('button', { name: 'Hide replies (1 loaded)', exact: true }).click();
  await root.getByRole('button', { name: 'Hide replies (2 loaded)', exact: true }).click();
  await root.getByRole('button', { name: 'Show replies (2 loaded)', exact: true }).click();
  await expect(child).toBeVisible();
  await expect(grandchild).not.toBeVisible();
  await child.getByRole('button', { name: 'Show replies (1 loaded)', exact: true }).click();
  await expect(grandchild).toBeVisible();
  await child.getByRole('button', { name: 'Reply', exact: true }).click();
  await expect(page.locator('.reply-context blockquote')).toHaveText(childBody);
  await page.getByRole('button', { name: 'Cancel reply', exact: true }).click();
  await expect(page.locator('.reply-context')).toHaveCount(0);
  await page.reload();
  await expect(page.locator('article.comment')).toHaveCount(20);
  await page.getByLabel('Add to the conversation').fill('A fictional unsent reply draft');
  await page.route(
    `**/api/posts/${post.id}/comments?cursor=*`,
    (route) =>
      route.fulfill({
        status: 410,
        contentType: 'application/json',
        body: JSON.stringify({ code: 'CURSOR_EXPIRED', title: 'Refresh this page to continue' }),
      }),
    { times: 1 },
  );
  await page.getByRole('button', { name: 'Load more comments', exact: true }).click();
  await expect(page.locator('.thread').getByRole('alert')).toHaveText(
    'Refresh this page to continue',
  );
  await expect(root).toBeVisible();
  await page.getByRole('button', { name: 'Refresh conversation', exact: true }).click();
  await expect(page.locator('.thread').getByRole('alert')).toHaveCount(0);
  await expect(page.getByLabel('Add to the conversation')).toHaveValue(
    'A fictional unsent reply draft',
  );
  await page.getByRole('button', { name: 'Load more comments', exact: true }).click();
  await expect(page.locator('article.comment')).toHaveCount(25);
  await page
    .getByTestId(`comment-${lateParent}`)
    .getByRole('button', { name: 'Reply', exact: true })
    .click();
  await expect(page.locator('.reply-context blockquote')).toHaveText(
    'Fictional later conversation entry 2',
  );
  await page.getByRole('button', { name: 'Refresh conversation', exact: true }).click();
  await expect(page.locator('article.comment')).toHaveCount(20);
  await expect(page.locator('.reply-context')).toContainText('Reply context unavailable');
  await expect(page.locator('.reply-context blockquote')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Submit comment', exact: true })).toBeDisabled();
  await page.getByRole('button', { name: 'Load more comments', exact: true }).click();
  await expect(page.locator('.reply-context blockquote')).toHaveText(
    'Fictional later conversation entry 2',
  );
  await expect(page.getByRole('button', { name: 'Submit comment', exact: true })).toBeEnabled();
  expect(
    (
      await neighbour.request.delete(`/api/comments/${lateParent}`, {
        headers: { 'x-jansetu-csrf': '1', 'if-match': '"2"' },
      })
    ).status(),
  ).toBe(204);
  await page.getByRole('button', { name: 'Refresh conversation', exact: true }).click();
  await page.getByRole('button', { name: 'Load more comments', exact: true }).click();
  await expect(page.locator('article.comment')).toHaveCount(25);
  await expect(page.locator('.reply-context blockquote')).toHaveCount(0);
  await expect(page.locator('.reply-context')).toContainText('Reply context unavailable');
  await expect(page.getByRole('button', { name: 'Submit comment', exact: true })).toBeDisabled();
  await page.getByRole('button', { name: 'Cancel reply', exact: true }).click();
  await root.getByRole('button', { name: 'Reply', exact: true }).click();
  await root.getByRole('button', { name: 'Delete', exact: true }).click();
  await page
    .getByRole('dialog', { name: 'Delete comment?' })
    .getByRole('button', { name: 'Delete comment', exact: true })
    .click();
  await expect(root.getByText('This comment was deleted.', { exact: true })).toBeVisible();
  await expect(root.getByText(rootBody, { exact: true })).toHaveCount(0);
  await expect(page.locator('.reply-context')).toHaveCount(0);
  await expect(
    child.getByRole('link', { name: 'Reply to a deleted comment', exact: true }),
  ).toBeVisible();
  await page.setViewportSize({ width: 320, height: 850 });
  await child.getByRole('button', { name: 'Reply', exact: true }).click();
  await expect(page.locator('.reply-context blockquote')).toHaveText(childBody);
  await expect
    .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
    .toBe(true);
  await page.locator('.reply-context').scrollIntoViewIfNeeded();
  await page.screenshot({ path: 'test-results/paginated-conversation-mobile.png' });
  const latest = (await (await page.request.get(`/api/posts/${post.id}`)).json()) as Schema['Post'];
  expect(
    (
      await page.request.delete(`/api/posts/${post.id}`, {
        headers: { 'x-jansetu-csrf': '1', 'if-match': `"${latest.version}"` },
      })
    ).status(),
  ).toBe(204);
  await other.close();
  await staff.context.close();
});
test('private report, triage, agency work, independent verification, and reviewed public progress', async ({
  page,
  browser,
}) => {
  await page.setViewportSize({ width: 1280, height: 640 });
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
  await coord.page
    .getByRole('textbox', { name: 'Private publication reason', exact: true })
    .fill('Private synthetic publication review');
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
  const profileName = await page.locator('.account-control strong').innerText();
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
  await expect(page.locator('.account-control strong')).toHaveText(profileName);
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
  const profileName = await page.locator('.account-control strong').innerText();
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
  // Exercise draft recovery while a fresh client session lookup is delayed.
  await page.route('**/api/me', async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 5000));
    await route.continue();
  });
  await page.reload();
  await expect(page.locator('.account-control strong')).toHaveText(profileName);
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

test('public profiles, people search, follows, blocks and unblocks survive navigation', async ({
  page,
  browser,
}) => {
  await page.goto('/');
  await signIn(page, 'Rohan Mehta');
  const ownerContext = await browser.newContext();
  const owner = await ownerContext.newPage();
  await owner.goto('/');
  await signIn(owner, 'Ananya Rao');
  const me = (await (await owner.request.get('/api/me')).json()) as Schema['Me'];
  const profileURL = `/profiles/${me.profile.id}`;
  // The named author link is a real route rather than a decorative byline.
  const feed = (await (await page.request.get('/api/feed')).json()) as Schema['Feed'];
  const ownPost = feed.items.find(
    (item) => item.type === 'POST' && item.post.author?.id === me.profile.id,
  );
  expect(ownPost?.type).toBe('POST');
  if (ownPost?.type !== 'POST') throw new Error('Published author fixture missing');
  await page.goto(`/posts/${ownPost.post.id}`);
  await page
    .getByTestId(`post-${ownPost.post.id}`)
    .getByRole('link', { name: me.profile.displayName, exact: true })
    .click();
  await expect(page).toHaveURL(profileURL);
  await expect(
    page.getByRole('heading', { name: me.profile.displayName, exact: true }),
  ).toBeVisible();
  await expect(page.getByText(me.profile.bio!, { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Follow person', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Unfollow person', exact: true })).toBeVisible();
  await page.reload();
  await expect(page.getByRole('button', { name: 'Unfollow person', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Unfollow person', exact: true }).click();
  await page.getByRole('button', { name: 'Follow person', exact: true }).click();
  await page.goto(`/search?q=${encodeURIComponent('@' + me.profile.handle)}`);
  await page
    .getByRole('region', { name: 'People search results' })
    .getByRole('link')
    .filter({ hasText: me.profile.displayName })
    .click();
  await page.getByRole('button', { name: 'Block person', exact: true }).click();
  const confirmation = page.getByRole('dialog', { name: 'Block this person?' });
  await confirmation.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: me.profile.displayName, exact: true }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Block person', exact: true }).click();
  await confirmation.getByRole('button', { name: 'Block person', exact: true }).click();
  await expect(page).toHaveURL('/account#blocked-people');
  const blocks = page.getByRole('region', { name: 'Blocked people', exact: true });
  await expect(blocks.getByText(me.profile.displayName, { exact: true })).toBeVisible();
  await page.setViewportSize({ width: 320, height: 720 });
  await blocks.scrollIntoViewIfNeeded();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: 'test-results/blocked-people-mobile.png' });
  await page.goto(profileURL);
  await expect(
    page.getByRole('heading', { name: 'Profile unavailable', exact: true }),
  ).toBeVisible();
  await page.goto(`/search?q=${encodeURIComponent('@' + me.profile.handle)}`);
  await expect(page.getByRole('region', { name: 'People search results' })).toHaveCount(0);
  const viewer = (await (await page.request.get('/api/me')).json()) as Schema['Me'];
  expect((await owner.request.get(`/api/profiles/${viewer.profile.id}`)).status()).toBe(404);
  await page.goto('/account');
  await page.reload();
  await blocks
    .getByRole('button', { name: `Unblock ${me.profile.displayName}`, exact: true })
    .click();
  await page
    .getByRole('dialog', { name: 'Unblock this person?' })
    .getByRole('button', { name: 'Unblock person', exact: true })
    .click();
  await expect(blocks.getByText(me.profile.displayName, { exact: true })).toHaveCount(0);
  await page.goto(profileURL);
  await expect(
    page.getByRole('heading', { name: me.profile.displayName, exact: true }),
  ).toBeVisible();
  await expect(page.getByRole('button', { name: 'Follow person', exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: 'test-results/public-profile-mobile.png' });
  await ownerContext.close();
});

test('public profile excludes pending content and opens the latest private revision for editing', async ({
  page,
  browser,
}) => {
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  const me = (await (await page.request.get('/api/me')).json()) as Schema['Me'];
  const profileURL = `/profiles/${me.profile.id}`;
  const publicBody = `Fictional public profile update ${Date.now()}`;
  const privateBody = `Unpublished private profile revision ${Date.now()}`;
  const response = await page.request.post('/api/posts', {
    headers: { 'x-jansetu-csrf': '1', 'idempotency-key': crypto.randomUUID() },
    data: {
      kind: 'SHORT',
      body: publicBody,
      languageTag: 'en-IN',
      mediaIds: [],
      submitForReview: true,
    },
  });
  expect(response.status()).toBe(201);
  const post = (await response.json()) as Schema['Post'];
  await page.goto('/account');
  await page.getByRole('link', { name: 'View public profile', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: me.profile.displayName, exact: true }),
  ).toBeVisible();
  await expect(page.getByText(publicBody, { exact: true })).toHaveCount(0);
  const staff = await staffPage(browser, 'Kiran Shah');
  const review = staff.page.getByTestId('review-card').filter({ hasText: publicBody });
  await review.getByLabel('Review reason').fill('Constructive fictional profile update');
  await review.getByRole('button', { name: 'Approve & publish' }).click();
  await expect(review).not.toBeVisible();
  await page.reload();
  await expect(page.getByText(publicBody, { exact: true })).toBeVisible();
  const current = (await (
    await page.request.get(`/api/posts/${post.id}`)
  ).json()) as Schema['Post'];
  const edited = await page.request.patch(`/api/posts/${post.id}`, {
    headers: { 'x-jansetu-csrf': '1', 'if-match': `"${current.version}"` },
    data: { body: privateBody, languageTag: 'en-IN', mediaIds: [], submitForReview: true },
  });
  expect(edited.status()).toBe(200);
  await page.reload();
  await expect(page.getByText(publicBody, { exact: true })).toBeVisible();
  await expect(page.getByText(privateBody, { exact: true })).toHaveCount(0);
  const anonymous = await browser.newContext();
  const visitor = await anonymous.newPage();
  await visitor.goto(profileURL);
  await expect(visitor.getByText(publicBody, { exact: true })).toBeVisible();
  await expect(visitor.getByText(privateBody, { exact: true })).toHaveCount(0);
  const card = page.getByTestId(`post-${post.id}`);
  await card.getByLabel('Post options').click();
  await card.getByRole('button', { name: 'Edit post', exact: true }).click();
  await expect(
    page.getByRole('dialog', { name: 'Edit your post' }).getByLabel('What’s on your mind?'),
  ).toHaveValue(privateBody);
  await page
    .getByRole('dialog', { name: 'Edit your post' })
    .getByRole('button', { name: 'Close dialog', exact: true })
    .click();
  await card.getByLabel('Post options').click();
  await card.getByRole('button', { name: 'Delete post', exact: true }).click();
  await page
    .getByRole('dialog', { name: 'Delete this post?' })
    .getByRole('button', { name: 'Delete post', exact: true })
    .click();
  await expect(page.getByTestId(`post-${post.id}`)).toHaveCount(0);
  await anonymous.close();
  await staff.context.close();
});

test('activity inbox uses approved replies, persists read state and hides revoked sources', async ({
  page,
  browser,
}) => {
  await page.goto('/activity');
  await expect(page.getByRole('heading', { name: 'Your activity lives here' })).toBeVisible();
  await signIn(page, 'Ananya Rao');
  const neighbour = await staffPage(browser, 'Rohan Mehta');
  const staff = await staffPage(browser, 'Kiran Shah');
  const csrf = { 'x-jansetu-csrf': '1' };
  async function approve(target: string) {
    const { items } = (await (await staff.page.request.get('/api/moderation')).json()) as {
      items: Schema['Review'][];
    };
    const item = items.find((row) => row.postId === target || row.commentId === target);
    if (!item) throw new Error('Missing activity fixture review');
    expect(
      (
        await staff.page.request.post(`/api/moderation/${item.id}/decisions`, {
          headers: { ...csrf, 'if-match': `"${item.version}"` },
          data: {
            action: 'ALLOW',
            reason: 'Constructive fictional activity fixture',
            targetRevision: item.targetRevision,
          },
        })
      ).status(),
    ).toBe(200);
  }
  const response = await page.request.post('/api/posts', {
    headers: { ...csrf, 'idempotency-key': crypto.randomUUID() },
    data: {
      kind: 'SHORT',
      body: `Fictional activity conversation ${Date.now()}`,
      languageTag: 'en-IN',
      mediaIds: [],
      submitForReview: true,
    },
  });
  expect(response.status()).toBe(201);
  const post = (await response.json()) as Schema['Post'];
  await approve(post.id);
  const candidate = await neighbour.page.request.post(`/api/posts/${post.id}/comments`, {
    headers: { ...csrf, 'idempotency-key': crypto.randomUUID() },
    data: {
      body: 'A private pending reply must not become an inbox preview.',
      languageTag: 'en-IN',
      parentId: null,
    },
  });
  expect(candidate.status()).toBe(201);
  const comment = (await candidate.json()) as { id: string };
  const activity = async () =>
    (await (
      await page.request.get('/api/me/activity?filter=SOCIAL')
    ).json()) as Schema['ActivityPage'];
  expect((await activity()).items.filter((n) => n.target.id === post.id)).toHaveLength(0);
  await approve(comment.id);
  await expect
    .poll(async () => (await activity()).items.filter((n) => n.target.id === post.id).length)
    .toBe(1);
  await page.goto('/activity');
  await page.getByRole('button', { name: 'Conversations', exact: true }).click();
  const card = page
    .getByTestId('activity-card')
    .filter({ has: page.locator(`a[href="/posts/${post.id}"]`) });
  await expect(card).toContainText('Replied to your conversation.');
  await expect(card).not.toContainText('private pending');
  await card.getByRole('button', { name: 'Mark as read', exact: true }).click();
  await expect(card.getByRole('button', { name: 'Mark as unread', exact: true })).toBeVisible();
  await page.reload();
  await page.getByRole('button', { name: 'Conversations', exact: true }).click();
  await expect(card.getByRole('button', { name: 'Mark as unread', exact: true })).toBeVisible();
  await card.getByRole('button', { name: 'Mark as unread', exact: true }).click();
  await expect(card.getByRole('button', { name: 'Mark as read', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Service progress', exact: true }).click();
  await expect(card).toHaveCount(0);
  await page.getByRole('button', { name: 'Conversations', exact: true }).click();
  await expect(card).toBeVisible();
  await page.setViewportSize({ width: 320, height: 720 });
  await expect(page.locator('.topbar .activity-bell')).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await page.getByRole('button', { name: 'Use dark theme' }).click();
  await page.screenshot({ path: 'test-results/activity-mobile-dark.png' });
  await card.getByRole('link', { name: /Replied to your conversation/ }).click();
  await page.waitForURL(`**/posts/${post.id}`);
  const writer = (await (await neighbour.page.request.get('/api/me')).json()) as Schema['Me'];
  expect(
    (
      await page.request.put(`/api/me/blocks/${writer.profile.id}`, {
        headers: csrf,
        data: { enabled: true },
      })
    ).status(),
  ).toBe(200);
  await page.goto('/activity');
  await page.getByRole('button', { name: 'Refresh activity' }).click();
  await expect(card).toHaveCount(0);
  expect(
    (
      await page.request.put(`/api/me/blocks/${writer.profile.id}`, {
        headers: csrf,
        data: { enabled: false },
      })
    ).status(),
  ).toBe(200);
  const thread = (await (
    await neighbour.page.request.get(`/api/posts/${post.id}/comments`)
  ).json()) as Schema['CommentPage'];
  const latest = thread.items.find((c) => c.id === comment.id);
  if (!latest) throw new Error('Missing reply fixture');
  expect(
    (
      await neighbour.page.request.delete(`/api/comments/${comment.id}`, {
        headers: { ...csrf, 'if-match': `"${latest.version}"` },
      })
    ).status(),
  ).toBe(204);
  await page.getByRole('button', { name: 'Refresh activity' }).click();
  await expect(card).toHaveCount(0);
  await neighbour.context.close();
  await staff.context.close();
});

test('case activity arrives only after reviewed publication and disappears on unfollow', async ({
  page,
  browser,
}) => {
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  const follower = await staffPage(browser, 'Rohan Mehta');
  const staff = await staffPage(browser, 'Kiran Shah');
  const officer = await staffPage(browser, 'City Works team');
  const csrf = { 'x-jansetu-csrf': '1' };
  const response = await page.request.post('/api/service-reports', {
    headers: { ...csrf, 'idempotency-key': crypto.randomUUID() },
    data: {
      clientSubmissionId: crypto.randomUUID(),
      statement: `Private activity report ${Date.now()}`,
      languageTag: 'en-IN',
      category: 'FOOTPATH',
      locationLabel: 'Fictional neighbourhood crossing',
      publicationPreference: 'SANITIZED_RECEIPT',
    },
  });
  expect(response.status()).toBe(201);
  const report = (await response.json()) as { id: string };
  const triage = await staff.page.request.post(`/api/authority/reports/${report.id}/triage`, {
    headers: { ...csrf, 'if-match': '"1"' },
    data: {
      agencyId: '30000000-0000-4000-8000-000000000001',
      category: 'FOOTPATH',
      urgencyTier: 2,
      reason: 'Fictional crossing restoration requires agency action',
    },
  });
  expect(triage.status()).toBe(201);
  const { caseId } = (await triage.json()) as { caseId: string };
  const publication = {
    title: `Reviewed activity crossing ${Date.now()}`,
    summary: 'A fictional restoration task was proposed.',
    area: 'Indiranagar',
    reviewed: true,
    publicationVersion: 0,
    reason: 'Private synthetic publication review',
  };
  const published = await staff.page.request.post(`/api/authority/cases/${caseId}/publications`, {
    headers: { ...csrf, 'if-match': '"1"' },
    data: publication,
  });
  expect(published.status()).toBe(200);
  const { receiptId } = (await published.json()) as { receiptId: string };
  expect(
    (
      await follower.page.request.put(`/api/case-receipts/${receiptId}/follow`, {
        headers: csrf,
        data: { following: true },
      })
    ).status(),
  ).toBe(200);
  const detail = (await (
    await officer.page.request.get(`/api/authority/cases/${caseId}`)
  ).json()) as Schema['CaseDetail'];
  const obligation = detail.obligations[0];
  expect(
    (
      await officer.page.request.post(`/api/authority/obligations/${obligation.id}/accept`, {
        headers: { ...csrf, 'if-match': `"${obligation.version}"` },
        data: { summary: 'Private agency acceptance statement' },
      })
    ).status(),
  ).toBe(200);
  const activity = async () =>
    (await (
      await follower.page.request.get('/api/me/activity?filter=CASES')
    ).json()) as Schema['ActivityPage'];
  expect((await activity()).items.filter((n) => n.target.id === receiptId)).toHaveLength(0);
  expect(
    (
      await staff.page.request.post(`/api/authority/cases/${caseId}/publications`, {
        headers: { ...csrf, 'if-match': '"2"' },
        data: {
          ...publication,
          publicationVersion: 1,
          summary: 'Agency acceptance was reviewed for public progress.',
        },
      })
    ).status(),
  ).toBe(200);
  await expect
    .poll(async () => (await activity()).items.filter((n) => n.target.id === receiptId).length)
    .toBe(1);
  const item = (await activity()).items.find((n) => n.target.id === receiptId);
  expect(JSON.stringify(item)).not.toContain(caseId);
  expect(JSON.stringify(item)).not.toContain(report.id);
  expect(JSON.stringify(item)).not.toContain('Private');
  await follower.page.goto('/activity');
  await follower.page.getByRole('button', { name: 'Service progress', exact: true }).click();
  const card = follower.page.getByTestId('activity-card').filter({ hasText: publication.title });
  await expect(card).toContainText('Reviewed public progress was updated.');
  await card.getByRole('link', { name: /Reviewed public progress/ }).click();
  await follower.page.waitForURL(`**/cases/${receiptId}`);
  await follower.page.getByRole('button', { name: 'Following', exact: true }).click();
  await follower.page.goto('/activity');
  await follower.page.getByRole('button', { name: 'Refresh activity' }).click();
  await expect(card).toHaveCount(0);
  await follower.context.close();
  await staff.context.close();
  await officer.context.close();
});

test('notification preferences persist, reject stale edits and gate worker delivery', async ({
  page,
  browser,
}) => {
  await page.goto('/account');
  await signIn(page, 'Ananya Rao');
  await page.goto('/account#activity-settings');
  const panel = page.locator('#activity-settings');
  const toggle = panel.getByRole('switch', { name: /In-app notifications/ });
  await expect(toggle).toBeChecked();
  await toggle.uncheck();
  const second = await staffPage(browser, 'Ananya Rao');
  const csrf = { 'x-jansetu-csrf': '1' };
  let current = (await (
    await second.page.request.get('/api/me/notification-preferences')
  ).json()) as Schema['NotificationPreference'];
  expect(
    (
      await second.page.request.patch('/api/me/notification-preferences', {
        headers: { ...csrf, 'if-match': `"${current.version}"` },
        data: { inApp: false },
      })
    ).status(),
  ).toBe(200);
  // Another session cannot silently overwrite the version of an unsaved edit.
  await panel.getByRole('button', { name: 'Save activity preferences' }).click();
  await expect(panel.getByRole('alert')).toContainText('This item changed');
  await expect(toggle).not.toBeChecked();
  await panel.getByRole('button', { name: 'Reload preferences' }).click();
  await expect(panel.getByRole('alert')).toHaveCount(0);
  await page.reload();
  await expect(toggle).not.toBeChecked();
  await page.goto('/activity');
  await expect(page.getByText('In-app notifications are paused.', { exact: false })).toBeVisible();
  expect((await (await page.request.get('/api/me/activity/summary')).json()).unreadCount).toBe(0);
  const neighbour = await staffPage(browser, 'Rohan Mehta');
  const staff = await staffPage(browser, 'Kiran Shah');
  async function approve(target: string) {
    const queue = (await (await staff.page.request.get('/api/moderation')).json()) as {
      items: Schema['Review'][];
    };
    const item = queue.items.find((m) => m.postId === target || m.commentId === target);
    if (!item) throw new Error('Missing preference fixture review');
    expect(
      (
        await staff.page.request.post(`/api/moderation/${item.id}/decisions`, {
          headers: { ...csrf, 'if-match': `"${item.version}"` },
          data: {
            action: 'ALLOW',
            reason: 'Constructive fictional preference fixture',
            targetRevision: item.targetRevision,
          },
        })
      ).status(),
    ).toBe(200);
  }
  const created = await page.request.post('/api/posts', {
    headers: { ...csrf, 'idempotency-key': crypto.randomUUID() },
    data: {
      kind: 'SHORT',
      body: `Fictional preference fixture ${Date.now()}`,
      languageTag: 'en-IN',
      mediaIds: [],
      submitForReview: true,
    },
  });
  expect(created.status()).toBe(201);
  const post = (await created.json()) as Schema['Post'];
  await approve(post.id);
  async function reply() {
    const response = await neighbour.page.request.post(`/api/posts/${post.id}/comments`, {
      headers: { ...csrf, 'idempotency-key': crypto.randomUUID() },
      data: {
        body: 'A fictional reply for preference delivery testing.',
        languageTag: 'en-IN',
        parentId: null,
      },
    });
    expect(response.status()).toBe(201);
    const result = (await response.json()) as { id: string };
    await approve(result.id);
  }
  await reply();
  await expect
    .poll(async () => {
      const detail = (await (
        await page.request.get(`/api/posts/${post.id}`)
      ).json()) as Schema['Post'];
      return detail.stats.comments;
    })
    .toBe(1); // The same worker transaction completes the notification projection.
  await page.goto('/account#activity-settings');
  await toggle.check();
  await panel.getByRole('button', { name: 'Save activity preferences' }).click();
  await expect(page.getByRole('status')).toContainText('In-app notifications enabled');
  const activity = async () =>
    (await (
      await page.request.get('/api/me/activity?filter=SOCIAL')
    ).json()) as Schema['ActivityPage'];
  expect((await activity()).items.filter((n) => n.target.id === post.id)).toHaveLength(0);
  await reply();
  await expect
    .poll(async () => (await activity()).items.filter((n) => n.target.id === post.id).length)
    .toBe(1);
  await page.reload();
  await expect(toggle).toBeChecked();
  await page.setViewportSize({ width: 320, height: 720 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await panel.scrollIntoViewIfNeeded();
  await page.screenshot({ path: 'test-results/activity-preferences-mobile.png' });
  await second.context.close();
  await neighbour.context.close();
  await staff.context.close();
});

test('person and community mutes preserve explicit access and can be managed from Account', async ({
  page,
  browser,
}) => {
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  const neighbour = await staffPage(browser, 'Rohan Mehta');
  const staff = await staffPage(browser, 'Kiran Shah');
  const csrf = { 'x-jansetu-csrf': '1' };
  const writer = (await (await neighbour.page.request.get('/api/me')).json()) as Schema['Me'];
  const marker = `Fictional mute discovery ${Date.now()}`;
  const communityId = '50000000-0000-4000-8000-000000000001';
  for (const [targetType, targetId] of [
    ['PROFILE', writer.profile.id],
    ['COMMUNITY', communityId],
  ]) {
    expect(
      (
        await page.request.put('/api/me/mutes', {
          headers: csrf,
          data: { targetType, targetId, active: false },
        })
      ).status(),
    ).toBe(200);
  }
  const community = (await (
    await neighbour.page.request.get(`/api/communities/${communityId}`)
  ).json()) as Schema['Community'];
  expect(
    (
      await neighbour.page.request.put(`/api/communities/${communityId}/membership`, {
        headers: csrf,
        data: { joined: true, rulesRevision: community.rulesRevision },
      })
    ).status(),
  ).toBe(200);
  const response = await neighbour.page.request.post('/api/posts', {
    headers: { ...csrf, 'idempotency-key': crypto.randomUUID() },
    data: {
      kind: 'SHORT',
      body: marker,
      languageTag: 'en-IN',
      mediaIds: [],
      communityId,
      submitForReview: true,
    },
  });
  expect(response.status()).toBe(201);
  const post = (await response.json()) as Schema['Post'];
  const queue = (await (await staff.page.request.get('/api/moderation')).json()) as {
    items: Schema['Review'][];
  };
  const review = queue.items.find((m) => m.postId === post.id);
  if (!review) throw new Error('Missing mute fixture review');
  expect(
    (
      await staff.page.request.post(`/api/moderation/${review.id}/decisions`, {
        headers: { ...csrf, 'if-match': `"${review.version}"` },
        data: {
          action: 'ALLOW',
          reason: 'Constructive fictional mute fixture',
          targetRevision: review.targetRevision,
        },
      })
    ).status(),
  ).toBe(200);
  await page.goto(`/posts/${post.id}`);
  // Full navigations reload the session independently of public content.
  await expect(page.locator('.account-control strong')).not.toHaveText('Explore JanSetu');
  await page.getByRole('button', { name: 'Bookmark post' }).click();
  await page.goto(`/profiles/${writer.profile.id}`);
  await expect(
    page.getByRole('heading', { name: writer.profile.displayName, exact: true }),
  ).toBeVisible();
  await expect(page.locator('.account-control strong')).not.toHaveText('Explore JanSetu');
  await expect(page.getByRole('button', { name: /^(Unfollow|Follow) person$/ })).toBeVisible();
  const follow = page.getByRole('button', { name: 'Follow person', exact: true });
  if (await follow.isVisible()) await follow.click();
  await expect(page.getByRole('button', { name: 'Unfollow person', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Mute person', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Mute this person?' });
  await dialog.getByLabel('Mute duration').selectOption('HOUR');
  await dialog.getByRole('button', { name: 'Mute person', exact: true }).click();
  await expect(dialog).not.toBeVisible();
  await expect(page.getByRole('button', { name: 'Unmute person', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Unfollow person', exact: true })).toBeVisible();
  await page.goto(`/search?q=${encodeURIComponent(marker)}`);
  await expect(page.getByText(marker, { exact: true })).toHaveCount(0);
  await page.goto('/bookmarks');
  await expect(page.getByText(marker, { exact: true })).toBeVisible();
  await page.goto(`/posts/${post.id}`);
  await expect(page.getByText(marker, { exact: true })).toBeVisible();
  await page.getByLabel('Post options').click();
  await expect(page.getByRole('button', { name: 'Unmute person', exact: true })).toBeVisible();
  await page.goto('/account#muted-items');
  const person = page.getByTestId('mute-row').filter({ hasText: writer.profile.displayName });
  await expect(person).toContainText('Muted until');
  await page.reload();
  await expect(person).toBeVisible();
  await person.getByRole('button', { name: /Remove mute/ }).click();
  await expect(person).toHaveCount(0);
  await page.goto(`/search?q=${encodeURIComponent(marker)}`);
  await expect(page.getByText(marker, { exact: true })).toBeVisible();
  await page.goto(`/posts/${post.id}`);
  await page.getByLabel('Post options').click();
  await page.getByRole('button', { name: 'Mute person', exact: true }).click();
  await expect(dialog).toBeVisible();
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(dialog).not.toBeVisible();
  await expect(page.locator('details.menu')).not.toHaveAttribute('open');
  await page.getByLabel('Post options').click();
  await page.getByRole('button', { name: 'Mute person', exact: true }).click();
  await expect(dialog).toBeVisible();
  await dialog.getByRole('button', { name: 'Mute person', exact: true }).click();
  await expect(dialog).not.toBeVisible();
  await page.getByLabel('Post options').click();
  await page.getByRole('button', { name: 'Unmute person', exact: true }).click();
  await expect
    .poll(async () => {
      const mutes = (await (await page.request.get('/api/me/mutes')).json()) as Schema['MutePage'];
      return mutes.items.some((item) => item.targetId === writer.profile.id);
    })
    .toBe(false);
  await page.goto(`/communities/${communityId}`);
  await page.getByRole('button', { name: 'Mute community', exact: true }).click();
  await page
    .getByRole('dialog', { name: 'Mute this community?' })
    .getByRole('button', { name: 'Mute community', exact: true })
    .click();
  await expect(page.getByRole('button', { name: 'Unmute community', exact: true })).toBeVisible();
  await expect(page.getByText(marker, { exact: true })).toHaveCount(0);
  await page.goto('/account#muted-items');
  const row = page.getByTestId('mute-row').filter({ hasText: community.title });
  await expect(row).toBeVisible();
  await page.setViewportSize({ width: 320, height: 720 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await row.scrollIntoViewIfNeeded();
  await page.screenshot({ path: 'test-results/mutes-mobile.png' });
  await row.getByRole('button', { name: /Remove mute/ }).click();
  await expect(row).toHaveCount(0);
  expect((await neighbour.page.request.get('/api/me/mutes')).status()).toBe(200);
  expect((await (await neighbour.page.request.get('/api/me/mutes')).json()).items).toHaveLength(0);
  await neighbour.context.close();
  await staff.context.close();
});

async function approveContentReportFixture(staff: Page, id: string) {
  const queue = (await (await staff.request.get('/api/moderation')).json()) as {
    items: Schema['Review'][];
  };
  const review = queue.items.find((item) => item.postId === id || item.commentId === id);
  if (!review) throw new Error('Missing content report publication review');
  expect(
    (
      await staff.request.post(`/api/moderation/${review.id}/decisions`, {
        headers: { 'x-jansetu-csrf': '1', 'if-match': `"${review.version}"` },
        data: {
          action: 'ALLOW',
          reason: 'Fictional content reporting fixture',
          targetRevision: review.targetRevision,
        },
      })
    ).status(),
  ).toBe(200);
}
async function publishedContentReportFixture(writer: Page, staff: Page, body: string) {
  const response = await writer.request.post('/api/posts', {
    headers: { 'x-jansetu-csrf': '1', 'idempotency-key': crypto.randomUUID() },
    data: { kind: 'SHORT', body, languageTag: 'en-IN', mediaIds: [], submitForReview: true },
  });
  expect(response.status()).toBe(201);
  const post = (await response.json()) as Schema['Post'];
  await approveContentReportFixture(staff, post.id);
  return (await (await writer.request.get(`/api/posts/${post.id}`)).json()) as Schema['Post'];
}
async function ownContentReportFor(page: Page, target: string) {
  const reports = (await (
    await page.request.get('/api/me/content-reports')
  ).json()) as Schema['ContentReportPage'];
  const item = reports.items.find((report) => report.targetId === target);
  if (!item) throw new Error('Missing private content report receipt');
  return item;
}

test('questions retain revision-bound helpful responses and recover a stale choice', async ({
  page,
  browser,
}) => {
  test.setTimeout(90_000);
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  const writer = await staffPage(browser, 'Rohan Mehta');
  const staff = await staffPage(browser, 'Kiran Shah');
  const created = await page.request.post('/api/posts', {
    headers: { 'x-jansetu-csrf': '1', 'idempotency-key': crypto.randomUUID() },
    data: {
      kind: 'QUESTION',
      communityId: '50000000-0000-4000-8000-000000000001',
      title: `Fictional helpful answer ${Date.now()}`,
      body: 'Where is the fictional community meeting?',
      languageTag: 'en-IN',
      mediaIds: [],
      submitForReview: true,
    },
  });
  expect(created.status()).toBe(201);
  const question = (await created.json()) as Schema['Post'];
  await approveContentReportFixture(staff.page, question.id);
  async function reply(body: string, parentId: string | null = null) {
    const response = await writer.page.request.post(`/api/posts/${question.id}/comments`, {
      headers: { 'x-jansetu-csrf': '1', 'idempotency-key': crypto.randomUUID() },
      data: { body, languageTag: 'en-IN', parentId },
    });
    expect(response.status()).toBe(201);
    const comment = (await response.json()) as { id: string };
    await approveContentReportFixture(staff.page, comment.id);
    return comment.id;
  }
  const first = await reply('The fictional meeting is in the community hall.');
  const second = await reply('A fictional follow-up includes the meeting time.', first);
  await page.goto(`/posts/${question.id}`);
  const summary = page.getByRole('complementary', { name: 'Helpful response', exact: true });
  const firstCard = page.getByTestId(`comment-${first}`);
  const secondCard = page.getByTestId(`comment-${second}`);
  await firstCard.getByRole('button', { name: 'Mark helpful', exact: true }).click();
  await expect(summary).toContainText('The fictional meeting is in the community hall.');
  await expect(firstCard.locator('.badge')).toHaveText('Helpful response');
  await firstCard.getByRole('button', { name: /Hide replies/ }).click();
  await expect(secondCard).toBeHidden();
  await expect(summary).toBeVisible();
  await firstCard.getByRole('button', { name: /Show replies/ }).click();
  await secondCard.getByRole('button', { name: 'Replace helpful response', exact: true }).click();
  await expect(summary).toContainText('A fictional follow-up includes the meeting time.');
  await expect(firstCard.locator('.badge')).toHaveCount(0);
  await page.reload();
  await expect(summary).toContainText('A fictional follow-up includes the meeting time.');
  await expect(summary.getByRole('button', { name: 'Clear helpful response' })).toBeVisible();
  await writer.page.goto(`/posts/${question.id}`);
  await expect(writer.page.getByRole('complementary', { name: 'Helpful response' })).toBeVisible();
  await expect(
    writer.page.getByRole('button', {
      name: /Mark helpful|Replace helpful response|Clear helpful response/,
    }),
  ).toHaveCount(0);
  // An edit awaiting review keeps the approved answer; approval invalidates the old choice.
  expect(
    (
      await writer.page.request.patch(`/api/comments/${second}`, {
        headers: { 'x-jansetu-csrf': '1', 'if-match': '"2"' },
        data: { body: 'PRIVATE PENDING HELPFUL ANSWER', languageTag: 'en-IN' },
      })
    ).status(),
  ).toBe(200);
  await page.reload();
  await expect(summary).toContainText('A fictional follow-up includes the meeting time.');
  await expect(summary).not.toContainText('PRIVATE PENDING HELPFUL ANSWER');
  await approveContentReportFixture(staff.page, second);
  await page.reload();
  await expect(summary).toHaveCount(0);
  await expect(secondCard).toContainText('PRIVATE PENDING HELPFUL ANSWER');
  // A second tab commits a choice after the first tab has loaded its version.
  const current = (await (
    await page.request.get(`/api/posts/${question.id}`)
  ).json()) as Schema['Post'];
  expect(
    (
      await page.request.put(`/api/posts/${question.id}/selected-response`, {
        headers: { 'x-jansetu-csrf': '1', 'if-match': `"${current.version}"` },
        data: { commentId: first, commentRevision: 1 },
      })
    ).status(),
  ).toBe(200);
  await secondCard.getByRole('button', { name: 'Mark helpful', exact: true }).click();
  await expect(page.locator('.thread').getByRole('alert')).toContainText('This item changed');
  await expect(summary).toContainText('The fictional meeting is in the community hall.');
  await secondCard.getByRole('button', { name: 'Replace helpful response', exact: true }).click();
  await expect(summary).toContainText('PRIVATE PENDING HELPFUL ANSWER');
  await page.setViewportSize({ width: 320, height: 740 });
  await expect(summary).toBeVisible();
  await summary.scrollIntoViewIfNeeded();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await page.screenshot({ path: 'test-results/helpful-response-mobile.png' });
  await page.getByRole('button', { name: 'Use dark theme', exact: true }).click();
  await expect(summary).toBeVisible();
  await summary.scrollIntoViewIfNeeded();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await page.screenshot({ path: 'test-results/helpful-response-mobile-dark.png' });
  await summary.getByRole('button', { name: 'Clear helpful response', exact: true }).click();
  await expect(summary).toHaveCount(0);
  await expect(secondCard.locator('.badge')).toHaveCount(0);
  // Approval can race a click without changing the post version. The UI must
  // refresh the reply and require a deliberate choice of its new revision.
  expect(
    (
      await writer.page.request.patch(`/api/comments/${first}`, {
        headers: { 'x-jansetu-csrf': '1', 'if-match': '"2"' },
        data: { body: 'A newly approved fictional meeting location.', languageTag: 'en-IN' },
      })
    ).status(),
  ).toBe(200);
  await approveContentReportFixture(staff.page, first);
  await firstCard.getByRole('button', { name: 'Mark helpful', exact: true }).click();
  await expect(page.locator('.thread').getByRole('alert')).toContainText('This item changed');
  await expect(summary).toHaveCount(0);
  await expect(firstCard).toContainText('A newly approved fictional meeting location.');
  await firstCard.getByRole('button', { name: 'Mark helpful', exact: true }).click();
  await expect(summary).toBeVisible();
  await expect(summary).toContainText('A newly approved fictional meeting location.');
  expect(
    (
      await writer.page.request.delete(`/api/comments/${first}`, {
        headers: { 'x-jansetu-csrf': '1', 'if-match': '"4"' },
      })
    ).status(),
  ).toBe(204);
  await page.reload();
  await expect(summary).toHaveCount(0);
  await expect(firstCard).toContainText('This comment was deleted.');
  await expect(secondCard).toContainText('PRIVATE PENDING HELPFUL ANSWER');
  await writer.context.close();
  await staff.context.close();
});

test('private content report receipts recover lost acknowledgements and retain dismissal outcomes', async ({
  page,
  browser,
}) => {
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  const writer = await staffPage(browser, 'Rohan Mehta');
  const mod = await staffPage(browser, 'Kiran Shah');
  const marker = `Fictional reported source ${Date.now()}`;
  const detail = `Private fictional report detail ${Date.now()}`;
  const post = await publishedContentReportFixture(writer.page, mod.page, marker);
  await page.goto(`/posts/${post.id}`);
  await page.getByLabel('Post options').click();
  await page.getByRole('button', { name: 'Report post', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Report this post?' });
  await expect(dialog).toBeVisible();
  await dialog.getByLabel('Report reason').selectOption('OTHER');
  await expect(dialog.getByRole('button', { name: 'Submit content report' })).toBeDisabled();
  await dialog.getByLabel(/Additional detail/).fill('😀😀😀');
  await expect(dialog.getByRole('button', { name: 'Submit content report' })).toBeDisabled();
  await dialog.getByLabel(/Additional detail/).fill('😀😀😀😀😀');
  await expect(dialog.getByRole('button', { name: 'Submit content report' })).toBeEnabled();
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(dialog).not.toBeVisible();
  await page.getByLabel('Post options').click();
  await page.getByRole('button', { name: 'Report post', exact: true }).click();
  await dialog.getByLabel('Report reason').selectOption('PRIVACY');
  await dialog.getByLabel(/Additional detail/).fill(detail);
  let lost = false;
  let allocated: string | undefined;
  const requestKeys: string[] = [];
  await page.route('**/api/content-reports', async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    requestKeys.push(route.request().headers()['idempotency-key']);
    const response = await route.fetch();
    if (!lost && response.status() === 201) {
      lost = true;
      allocated = ((await response.json()) as Schema['ContentReportReceipt']).id;
      await route.fulfill({
        status: 503,
        json: {
          status: 503,
          code: 'LOST_ACKNOWLEDGEMENT',
          title: 'Please retry your content report',
          retryable: true,
        },
      });
    } else await route.fulfill({ response });
  });
  await dialog.getByRole('button', { name: 'Submit content report' }).click();
  await expect(dialog.getByRole('alert')).toContainText('Please retry');
  await expect(dialog.getByLabel(/Additional detail/)).toHaveValue(detail);
  await dialog.getByRole('button', { name: 'Submit content report' }).click();
  await expect(page.getByRole('dialog', { name: 'Content report received' })).toBeVisible();
  expect(requestKeys).toHaveLength(2);
  expect(requestKeys[0]).toBe(requestKeys[1]);
  const receipt = await ownContentReportFor(page, post.id);
  expect(receipt.id).toBe(allocated);
  expect(receipt.state).toBe('OPEN');
  expect((await writer.page.request.get(`/api/me/content-reports/${receipt.id}`)).status()).toBe(
    404,
  );
  const publicSource = await (await writer.page.request.get(`/api/posts/${post.id}`)).json();
  expect(JSON.stringify(publicSource)).not.toContain(detail);
  expect(JSON.stringify(publicSource)).not.toContain(receipt.id);
  await page.getByRole('link', { name: 'View my content reports' }).click();
  const own = page.getByTestId(`content-report-${receipt.id}`);
  await expect(own).toContainText(detail);
  await expect(own).toContainText(marker);
  await page.reload();
  await expect(own).toBeVisible();
  await mod.page.goto('/studio');
  await mod.page.getByRole('button', { name: 'Reported content', exact: true }).click();
  const card = mod.page.getByTestId(`content-report-review-${receipt.id}`);
  await expect(card).toContainText(marker);
  await expect(card).not.toContainText('Ananya Rao');
  await card.getByLabel('Decision reason').fill('😀😀😀');
  await expect(card.getByRole('button', { name: 'Dismiss report', exact: true })).toBeDisabled();
  await expect(
    card.getByRole('button', { name: 'Remove reported content', exact: true }),
  ).toBeDisabled();
  await card.getByLabel('Decision reason').fill('😀😀😀😀😀');
  await card.getByLabel('Reason shared with author').fill('😀😀😀😀😀');
  await expect(card.getByRole('button', { name: 'Dismiss report', exact: true })).toBeEnabled();
  await expect(
    card.getByRole('button', { name: 'Remove reported content', exact: true }),
  ).toBeEnabled();
  await card.getByLabel('Decision reason').fill('The fictional report does not warrant removal.');
  await card.getByRole('button', { name: 'Dismiss report', exact: true }).click();
  await expect(card).toHaveCount(0);
  await page.reload();
  await expect(own).toContainText('Report dismissed');
  await expect(own).toContainText('does not warrant removal');
  expect((await page.request.get(`/api/posts/${post.id}`)).status()).toBe(200);
  await page.goto(`/posts/${post.id}`);
  await page.getByLabel('Post options').click();
  await page.getByRole('button', { name: 'Report post', exact: true }).click();
  await dialog.getByLabel('Report reason').selectOption('PRIVACY');
  await dialog.getByLabel(/Additional detail/).fill(detail);
  await dialog.getByRole('button', { name: 'Submit content report' }).click();
  await expect(page.getByRole('dialog', { name: 'Report outcome available' })).toContainText(
    'existing private receipt',
  );
  expect((await ownContentReportFor(page, post.id)).id).toBe(receipt.id);
  await page.getByRole('link', { name: 'View my content reports' }).click();
  await page.setViewportSize({ width: 320, height: 720 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await own.scrollIntoViewIfNeeded();
  await page.screenshot({ path: 'test-results/content-report-receipt-mobile.png' });
  await writer.context.close();
  await mod.context.close();
});

test('reviewed removal revokes published posts and comments without reviving edits or deleting replies', async ({
  page,
  browser,
}) => {
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  const writer = await staffPage(browser, 'Rohan Mehta');
  const mod = await staffPage(browser, 'Kiran Shah');
  const csrf = { 'x-jansetu-csrf': '1' };
  const marker = `Fictional removal source ${Date.now()}`;
  const post = await publishedContentReportFixture(writer.page, mod.page, marker);
  await page.goto(`/posts/${post.id}`);
  await page.getByRole('button', { name: 'Bookmark post' }).click();
  await page.getByLabel('Post options').click();
  await page.getByRole('button', { name: 'Report post', exact: true }).click();
  await page
    .getByRole('dialog', { name: 'Report this post?' })
    .getByRole('button', { name: 'Submit content report' })
    .click();
  await page
    .getByRole('dialog', { name: 'Content report received' })
    .getByRole('button', { name: 'Done', exact: true })
    .click();
  const receipt = await ownContentReportFor(page, post.id);
  expect(
    (
      await writer.page.request.patch(`/api/posts/${post.id}`, {
        headers: { ...csrf, 'if-match': `"${post.version}"` },
        data: {
          body: 'Private pending edit must not replace reported content',
          languageTag: 'en-IN',
          mediaIds: [],
          submitForReview: true,
        },
      })
    ).status(),
  ).toBe(200);
  const pendingQueue = (await (await mod.page.request.get('/api/moderation')).json()) as {
    items: Schema['Review'][];
  };
  const pending = pendingQueue.items.find((r) => r.postId === post.id && r.targetRevision === 2);
  if (!pending) throw new Error('Missing pending edit fixture');
  await mod.page.goto('/studio');
  await mod.page.getByRole('button', { name: 'Reported content', exact: true }).click();
  const card = mod.page.getByTestId(`content-report-review-${receipt.id}`);
  await expect(card).toContainText(marker);
  await expect(card).not.toContainText('Private pending edit');
  await card
    .getByLabel('Decision reason')
    .fill('Reviewed fictional post removal under local policy.');
  await card
    .getByLabel('Reason shared with author')
    .fill('Please follow the local community posting rule.');
  await card.getByRole('button', { name: 'Remove reported content' }).click();
  let confirmation = mod.page.getByRole('dialog', { name: 'Remove reported content?' });
  await confirmation.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(card).toBeVisible();
  await card.getByRole('button', { name: 'Remove reported content' }).click();
  await confirmation.getByRole('button', { name: 'Confirm removal' }).click();
  await expect(card).toHaveCount(0);
  expect((await page.request.get(`/api/posts/${post.id}`)).status()).toBe(404);
  expect(
    (
      await mod.page.request.post(`/api/moderation/${pending.id}/decisions`, {
        headers: { ...csrf, 'if-match': `"${pending.version}"` },
        data: {
          action: 'ALLOW',
          reason: 'Cannot revive removed published content',
          targetRevision: 2,
        },
      })
    ).status(),
  ).toBe(409);
  await page.goto('/bookmarks');
  await expect(page.getByText(marker, { exact: true })).toHaveCount(0);
  const removed = await ownContentReportFor(page, post.id);
  expect(removed.decision?.action).toBe('REMOVE');
  expect(removed.target).toBeNull();
  const thread = await publishedContentReportFixture(
    page,
    mod.page,
    `Fictional retained discussion ${Date.now()}`,
  );
  async function addReply(author: Page, body: string, parentId: string | null) {
    const response = await author.request.post(`/api/posts/${thread.id}/comments`, {
      headers: { ...csrf, 'idempotency-key': crypto.randomUUID() },
      data: { body, languageTag: 'en-IN', parentId },
    });
    expect(response.status()).toBe(201);
    const result = (await response.json()) as { id: string };
    await approveContentReportFixture(mod.page, result.id);
    return result.id;
  }
  const commentBody = `Fictional reported comment ${Date.now()}`;
  const comment = await addReply(writer.page, commentBody, null);
  const childBody = `Fictional retained reply ${Date.now()}`;
  const child = await addReply(page, childBody, comment);
  await expect
    .poll(async () => {
      const activity = (await (
        await page.request.get('/api/me/activity?filter=SOCIAL')
      ).json()) as Schema['ActivityPage'];
      return activity.items.some((n) => n.target.id === thread.id);
    })
    .toBe(true);
  await page.goto(`/posts/${thread.id}`);
  await page
    .getByTestId(`comment-${comment}`)
    .getByRole('button', { name: 'Report comment', exact: true })
    .click();
  const dialog = page.getByRole('dialog', { name: 'Report this comment?' });
  await dialog.getByLabel('Report reason').selectOption('HARASSMENT');
  await dialog.getByRole('button', { name: 'Submit content report' }).click();
  await page
    .getByRole('dialog', { name: 'Content report received' })
    .getByRole('button', { name: 'Done', exact: true })
    .click();
  const commentReceipt = await ownContentReportFor(page, comment);
  await mod.page.getByRole('button', { name: 'Refresh reported content' }).click();
  const commentCard = mod.page.getByTestId(`content-report-review-${commentReceipt.id}`);
  await expect(commentCard).toContainText(commentBody);
  await commentCard
    .getByLabel('Decision reason')
    .fill('Reviewed fictional comment removal under local policy.');
  await commentCard
    .getByLabel('Reason shared with author')
    .fill('Please follow the local community reply rule.');
  await mod.page.getByRole('button', { name: 'Use dark theme', exact: true }).click();
  await mod.page.setViewportSize({ width: 320, height: 720 });
  expect(
    await mod.page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
  ).toBe(true);
  await commentCard.getByRole('button', { name: 'Remove reported content' }).click();
  confirmation = mod.page.getByRole('dialog', { name: 'Remove reported content?' });
  await expect(confirmation).toBeVisible();
  expect(
    await mod.page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
  ).toBe(true);
  await mod.page.screenshot({ path: 'test-results/content-report-review-mobile-dark.png' });
  await confirmation.getByRole('button', { name: 'Confirm removal' }).click();
  await expect(commentCard).toHaveCount(0);
  await page.reload();
  await expect(page.getByTestId(`comment-${comment}`)).toHaveCount(0);
  await expect(page.getByTestId(`comment-${child}`)).toContainText(childBody);
  expect(
    (
      await page.request.post(`/api/posts/${thread.id}/comments`, {
        headers: { ...csrf, 'idempotency-key': crypto.randomUUID() },
        data: { body: 'Cannot reply to removed comment', languageTag: 'en-IN', parentId: comment },
      })
    ).status(),
  ).toBe(422);
  const activity = (await (
    await page.request.get('/api/me/activity?filter=SOCIAL')
  ).json()) as Schema['ActivityPage'];
  expect(activity.items.some((n) => n.target.id === thread.id)).toBe(false);
  await writer.context.close();
  await mod.context.close();
});

test('authors see private moderation decisions and safely correct rejected initial posts and replies', async ({
  page,
  browser,
}) => {
  test.setTimeout(90_000);
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  const staff = await staffPage(browser, 'Kiran Shah');
  const marker = `Initial private correction ${Date.now()}`;
  const sharedReason = 'Remove the private contact details before resubmitting.';
  const internalReason = `PRIVATE INTERNAL REVIEW ${Date.now()}`;
  const created = await page.request.post('/api/posts', {
    headers: { 'x-jansetu-csrf': '1', 'idempotency-key': crypto.randomUUID() },
    data: { kind: 'SHORT', body: marker, submitForReview: true },
  });
  expect(created.status()).toBe(201);
  const post = (await created.json()) as Schema['Post'];
  await staff.page.reload();
  let review = staff.page.getByTestId('review-card').filter({ hasText: marker });
  await expect(review).toBeVisible();
  await review.getByLabel('Review reason').fill(internalReason);
  await expect(review.getByRole('button', { name: 'Restrict revision' })).toBeDisabled();
  await review.getByLabel('Reason shared with author').fill(sharedReason);
  await review.getByRole('button', { name: 'Restrict revision' }).click();
  await expect(review).toHaveCount(0);
  const history = (await (
    await page.request.get('/api/me/moderation-decisions')
  ).json()) as Schema['AuthorModerationDecisionPage'];
  const decision = history.items.find((d) => d.target.id === post.id);
  if (!decision) throw new Error('Missing private author decision');
  expect(decision.reason).toBe(sharedReason);
  expect(JSON.stringify(history)).not.toContain(internalReason);
  expect(JSON.stringify(history)).not.toContain(marker);
  expect(
    (await staff.page.request.get(`/api/me/moderation-decisions/${decision.id}`)).status(),
  ).toBe(404);
  await page.goto('/account#moderation-decisions');
  const card = page.getByTestId(`moderation-decision-${decision.id}`);
  await expect(card).toContainText(sharedReason);
  await expect(card).toContainText('Revision restricted');
  await page.setViewportSize({ width: 320, height: 740 });
  await card.evaluate((el) =>
    window.scrollTo({
      top: el.getBoundingClientRect().top + window.scrollY - 150,
      behavior: 'instant',
    }),
  );
  await expect(card).toBeInViewport();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await page.screenshot({ path: 'test-results/author-moderation-mobile.png' });
  await page.getByRole('button', { name: 'Use dark theme', exact: true }).click();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await page.screenshot({ path: 'test-results/author-moderation-mobile-dark.png' });
  await card.getByRole('link', { name: 'Open current thread' }).click();
  await expect(page.locator('.account-control strong')).not.toHaveText('Explore JanSetu');
  await expect(page.locator('.post-body')).toContainText(marker);
  await page.getByLabel('Post options').click();
  await page.getByRole('button', { name: 'Edit post', exact: true }).click();
  const edit = page.getByRole('dialog', { name: 'Edit your post' });
  await edit.getByRole('textbox').fill('Corrected initial post without private contact details.');
  await edit.getByRole('button', { name: 'Submit for review', exact: true }).click();
  await expect(page.locator('.post-body')).toContainText('Corrected initial post');
  await expect(
    page.getByText('Only you can see this post until a moderator approves it.'),
  ).toBeVisible();
  await approveContentReportFixture(staff.page, post.id);
  await page.reload();
  await expect(page.locator('.account-control strong')).not.toHaveText('Explore JanSetu');
  await expect(page.locator('.post-body')).toContainText('Corrected initial post');
  const replyMarker = `Initial rejected reply ${Date.now()}`;
  const replyResponse = await page.request.post(`/api/posts/${post.id}/comments`, {
    headers: { 'x-jansetu-csrf': '1', 'idempotency-key': crypto.randomUUID() },
    data: { body: replyMarker },
  });
  expect(replyResponse.status()).toBe(201);
  const reply = (await replyResponse.json()) as { id: string };
  await staff.page.reload();
  review = staff.page.getByTestId('review-card').filter({ hasText: replyMarker });
  await review.getByLabel('Review reason').fill(internalReason);
  await review.getByLabel('Reason shared with author').fill(sharedReason);
  await review.getByRole('button', { name: 'Restrict revision' }).click();
  await expect(review).toHaveCount(0);
  await page.reload();
  const replyCard = page.getByTestId(`comment-${reply.id}`);
  await expect(replyCard).toContainText(replyMarker);
  await replyCard.getByRole('button', { name: 'Edit and resubmit', exact: true }).click();
  const replyEdit = page.getByRole('dialog', { name: 'Edit comment' });
  await replyEdit
    .getByRole('textbox')
    .fill('Corrected initial reply without private contact details.');
  await replyEdit.getByRole('button', { name: 'Submit edit for review', exact: true }).click();
  await expect(replyCard).toContainText('awaiting review');
  await approveContentReportFixture(staff.page, reply.id);
  await page.reload();
  await expect(replyCard).toContainText('Corrected initial reply');
  await expect(replyCard).not.toContainText(replyMarker);
  await page.goto('/account#moderation-decisions');
  const decisions = page.locator('#moderation-decisions');
  await expect(decisions).toContainText('Approved for publication');
  await expect(decisions).toContainText(sharedReason);
  await expect(decisions).not.toContainText(internalReason);
  await staff.context.close();
});

async function rejectedAppealFixture(owner: Page, staff: Page, marker: string) {
  const response = await owner.request.post('/api/posts', {
    headers: { 'x-jansetu-csrf': '1', 'idempotency-key': crypto.randomUUID() },
    data: { kind: 'SHORT', body: marker, submitForReview: true },
  });
  expect(response.status()).toBe(201);
  const post = (await response.json()) as Schema['Post'];
  await staff.reload();
  const card = staff.getByTestId('review-card').filter({ hasText: marker });
  await card.getByLabel('Review reason').fill('PRIVATE ORIGINAL APPEAL REVIEW NOTE');
  await card
    .getByLabel('Reason shared with author')
    .fill('Please check this fictional publication policy.');
  await card.getByRole('button', { name: 'Restrict revision' }).click();
  await expect(card).toHaveCount(0);
  const history = (await (
    await owner.request.get('/api/me/moderation-decisions')
  ).json()) as Schema['AuthorModerationDecisionPage'];
  const decision = history.items.find((d) => d.target.id === post.id);
  if (!decision) throw new Error('Missing appeal decision fixture');
  return { post, decision };
}
test('private appeals retry safely and an independent reviewer restores the exact rejected revision', async ({
  page,
  browser,
}) => {
  test.setTimeout(90_000);
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  const original = await staffPage(browser, 'Kiran Shah');
  const reviewer = await staffPage(browser, 'Neha Sen');
  const marker = `Independent appeal original ${Date.now()}`;
  const { post, decision } = await rejectedAppealFixture(page, original.page, marker);
  await page.goto('/account#moderation-decisions');
  const notice = page.getByTestId(`moderation-decision-${decision.id}`);
  await notice.getByRole('button', { name: 'Appeal decision', exact: true }).click();
  let dialog = page.getByRole('dialog', { name: 'Appeal this decision' });
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await notice.getByRole('button', { name: 'Appeal decision', exact: true }).click();
  dialog = page.getByRole('dialog', { name: 'Appeal this decision' });
  await expect(dialog.getByRole('button', { name: 'Submit appeal', exact: true })).toBeDisabled();
  const grounds = 'This fictional text follows the rule. Please review independently.';
  await dialog.getByLabel('Appeal grounds').fill(grounds);
  let first = true;
  const keys: string[] = [];
  const pattern = `**/api/moderation/decisions/${decision.id}/appeals`;
  await page.route(pattern, async (route) => {
    keys.push(route.request().headers()['idempotency-key']);
    const response = await route.fetch();
    if (first) {
      first = false;
      await route.fulfill({
        status: 503,
        contentType: 'application/json',
        body: JSON.stringify({
          code: 'ACK_LOST',
          title: 'Acknowledgement lost. Retry your appeal.',
        }),
      });
    } else await route.fulfill({ response });
  });
  await dialog.getByRole('button', { name: 'Submit appeal', exact: true }).click();
  await expect(dialog).toContainText('Acknowledgement lost');
  await dialog.getByRole('button', { name: 'Submit appeal', exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await page.unroute(pattern);
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
  const appeals = (await (
    await page.request.get('/api/me/appeals')
  ).json()) as Schema['AppealPage'];
  const matches = appeals.items.filter((v) => v.decisionId === decision.id);
  expect(matches).toHaveLength(1);
  const appeal = matches[0];
  expect(JSON.stringify(appeals)).not.toContain(marker);
  expect(JSON.stringify(appeals)).not.toContain('PRIVATE ORIGINAL APPEAL REVIEW NOTE');
  expect((await original.page.request.get(`/api/moderation/appeals/${appeal.id}`)).status()).toBe(
    404,
  );
  expect((await reviewer.page.request.get(`/api/me/appeals/${appeal.id}`)).status()).toBe(404);
  await reviewer.page.getByRole('button', { name: 'Appeals', exact: true }).click();
  let review = reviewer.page.getByTestId(`appeal-review-${appeal.id}`);
  await expect(review).toContainText(marker);
  await expect(review).toContainText(grounds);
  await review.getByRole('button', { name: 'Take review', exact: true }).click();
  await expect(review.getByLabel('Reason shared with author')).toBeVisible();
  await review
    .getByLabel('Reason shared with author')
    .fill('Independent review found this fictional revision follows the rule.');
  const assignedToast = reviewer.page.getByRole('button', {
    name: 'Dismiss notification',
    exact: true,
  });
  if (await assignedToast.isVisible()) await assignedToast.click();
  await reviewer.page.setViewportSize({ width: 320, height: 740 });
  await review.evaluate((el) =>
    window.scrollTo({
      top: el.getBoundingClientRect().top + window.scrollY - 150,
      behavior: 'instant',
    }),
  );
  await expect(review).toBeInViewport();
  expect(
    await reviewer.page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
  ).toBe(true);
  await reviewer.page.screenshot({ path: 'test-results/appeal-review-mobile.png' });
  await reviewer.page.getByRole('button', { name: 'Use dark theme', exact: true }).click();
  await reviewer.page.screenshot({ path: 'test-results/appeal-review-mobile-dark.png' });
  await review.getByRole('button', { name: 'Overturn decision', exact: true }).click();
  const confirm = reviewer.page.getByRole('dialog', { name: 'Overturn this decision?' });
  await expect(confirm).toContainText('exact reviewed revision');
  await confirm.getByRole('button', { name: 'Confirm appeal outcome', exact: true }).click();
  await expect(review).toHaveCount(0);
  await page.getByRole('button', { name: 'Refresh appeals', exact: true }).click();
  const receipt = page.getByTestId(`appeal-${appeal.id}`);
  await expect(receipt).toContainText('Decision overturned');
  await expect(receipt).toContainText('Reviewed revision published.');
  await expect(receipt).not.toContainText(marker);
  await page.setViewportSize({ width: 320, height: 740 });
  await receipt.evaluate((el) =>
    window.scrollTo({
      top: el.getBoundingClientRect().top + window.scrollY - 150,
      behavior: 'instant',
    }),
  );
  await expect(receipt).toBeInViewport();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  const toast = page.getByRole('button', { name: 'Dismiss notification', exact: true });
  if (await toast.isVisible()) await toast.click();
  await page.screenshot({ path: 'test-results/appeal-receipt-mobile.png' });
  const publicPost = await page.request.get(`/api/posts/${post.id}`);
  expect(publicPost.status()).toBe(200);
  expect(await publicPost.text()).toContain(marker);
  await original.context.close();
  await reviewer.context.close();
});
test('appeal reviewers recover stale source context and never publish a later pending edit', async ({
  page,
  browser,
}) => {
  test.setTimeout(90_000);
  await page.goto('/');
  await signIn(page, 'Rohan Mehta');
  const original = await staffPage(browser, 'Kiran Shah');
  const reviewer = await staffPage(browser, 'Neha Sen');
  const marker = `Stale appeal original ${Date.now()}`;
  const { post, decision } = await rejectedAppealFixture(page, original.page, marker);
  const created = await page.request.post(`/api/moderation/decisions/${decision.id}/appeals`, {
    headers: { 'x-jansetu-csrf': '1', 'idempotency-key': crypto.randomUUID() },
    data: { grounds: 'Please independently review this fictional restriction.' },
  });
  expect(created.status()).toBe(201);
  const appeal = (await created.json()) as Schema['AppealReceipt'];
  await reviewer.page.getByRole('button', { name: 'Appeals', exact: true }).click();
  const card = reviewer.page.getByTestId(`appeal-review-${appeal.id}`);
  await card.getByRole('button', { name: 'Take review', exact: true }).click();
  await card
    .getByLabel('Reason shared with author')
    .fill('The original decision is overturned; the later edit still needs its own review.');
  const current = (await (
    await page.request.get(`/api/posts/${post.id}`)
  ).json()) as Schema['Post'];
  const later = `LATER PRIVATE CANDIDATE ${Date.now()}`;
  const edit = await page.request.patch(`/api/posts/${post.id}`, {
    headers: { 'x-jansetu-csrf': '1', 'if-match': `"${current.version}"` },
    data: { title: 'Later edit', body: later, submitForReview: true },
  });
  expect(edit.status()).toBe(200);
  await card.getByRole('button', { name: 'Overturn decision', exact: true }).click();
  let dialog = reviewer.page.getByRole('dialog', { name: 'Overturn this decision?' });
  await dialog.getByRole('button', { name: 'Confirm appeal outcome', exact: true }).click();
  await expect(dialog).toContainText('source changed');
  await dialog.getByRole('button', { name: 'Refresh review context', exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(card).toContainText('later revision');
  await expect(card).not.toContainText(later);
  await card
    .getByLabel('Reason shared with author')
    .fill('The original decision is overturned; the later edit still needs its own review.');
  await card.getByRole('button', { name: 'Overturn decision', exact: true }).click();
  dialog = reviewer.page.getByRole('dialog', { name: 'Overturn this decision?' });
  await expect(dialog).toContainText('content will remain unavailable');
  await dialog.getByRole('button', { name: 'Confirm appeal outcome', exact: true }).click();
  await expect(card).toHaveCount(0);
  await page.goto('/account#appeals');
  const receipt = page.getByTestId(`appeal-${appeal.id}`);
  await expect(receipt).toContainText('Content was not restored');
  await expect(receipt).toContainText('later revision');
  await expect(receipt).not.toContainText(later);
  expect((await original.page.request.get(`/api/posts/${post.id}`)).status()).toBe(404);
  const outcome = (await (
    await page.request.get(`/api/me/appeals/${appeal.id}`)
  ).json()) as Schema['AppealReceipt'];
  expect(outcome.outcome?.restorationReason).toBe('TARGET_CHANGED');
  await original.context.close();
  await reviewer.context.close();
});

async function privateReviewNotice(page: Page, target: string) {
  await expect
    .poll(
      async () => {
        const response = await page.request.get('/api/me/activity?filter=MODERATION');
        if (!response.ok()) return false;
        const activity = (await response.json()) as Schema['ActivityPage'];
        return activity.items.some((n) => n.target.id === target);
      },
      { timeout: 15_000 },
    )
    .toBe(true);
}
function reviewActivityCard(
  page: Page,
  kind: 'moderation-decisions' | 'appeals' | 'content-reports',
  id: string,
) {
  return page
    .getByTestId('activity-card')
    .filter({ has: page.locator(`a[href="/account/${kind}/${id}"]`) });
}

async function authorApproval(page: Page, target: string, revision: number) {
  const history = (await (
    await page.request.get('/api/me/moderation-decisions')
  ).json()) as Schema['AuthorModerationDecisionPage'];
  const decision = history.items.find(
    (d) => d.target.id === target && d.target.revision === revision && d.action === 'ALLOW',
  );
  if (!decision) throw new Error('Missing exact publication approval');
  return decision;
}

test('publication approvals open exact private revisions and survive source deletion', async ({
  page,
  browser,
}) => {
  test.setTimeout(90_000);
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  const staff = await staffPage(browser, 'Kiran Shah');
  const other = await staffPage(browser, 'Rohan Mehta');
  const marker = `Private approval source ${Date.now()}`;
  const internal = `PRIVATE PUBLICATION REVIEW NOTE ${Date.now()}`;
  const created = await page.request.post('/api/posts', {
    headers: { 'X-JanSetu-CSRF': '1', 'Idempotency-Key': crypto.randomUUID() },
    data: { kind: 'SHORT', body: marker, languageTag: 'en-IN', submitForReview: true },
  });
  expect(created.status()).toBe(201);
  const post = (await created.json()) as Schema['Post'];
  const pending = (await (
    await page.request.get('/api/me/moderation-decisions')
  ).json()) as Schema['AuthorModerationDecisionPage'];
  expect(pending.items.some((d) => d.target.id === post.id)).toBe(false);
  await staff.page.reload();
  let review = staff.page.getByTestId('review-card').filter({ hasText: marker });
  await review.getByLabel('Review reason').fill(internal);
  await review.getByRole('button', { name: 'Approve & publish', exact: true }).click();
  await expect(review).toHaveCount(0);
  const first = await authorApproval(page, post.id, 1);
  await privateReviewNotice(page, first.id);
  const alerts = (await (
    await page.request.get('/api/me/activity?filter=MODERATION')
  ).json()) as Schema['ActivityPage'];
  const notice = alerts.items.find((n) => n.target.id === first.id);
  expect(notice?.kind).toBe('PUBLICATION_APPROVAL');
  expect(notice?.actor).toBeNull();
  for (const secret of [marker, internal, post.id])
    expect(JSON.stringify(alerts)).not.toContain(secret);
  await page.goto('/activity');
  await page.getByRole('button', { name: 'Moderation', exact: true }).click();
  const firstCard = reviewActivityCard(page, 'moderation-decisions', first.id);
  await expect(firstCard).toContainText('Publication review');
  await expect(firstCard).toContainText('A publication approval is available for your content.');
  await expect(firstCard).toHaveClass(/unread/);
  await firstCard.locator('.activity-target').click();
  await expect(page).toHaveURL(`/account/moderation-decisions/${first.id}`);
  const exact = page.getByTestId(`moderation-decision-${first.id}`);
  await expect(exact).toContainText('Approved for publication');
  await expect(exact).toContainText('REVISION 1');
  await expect(exact).toContainText('This revision was approved for publication.');
  await expect(exact).not.toContainText(internal);
  await expect(exact).not.toContainText(marker);
  await expect(exact.getByRole('button', { name: 'Appeal decision', exact: true })).toHaveCount(0);
  await other.page.goto(`/account/moderation-decisions/${first.id}`);
  await expect(
    other.page.getByRole('heading', { name: 'This private record is unavailable' }),
  ).toBeVisible();
  expect((await staff.page.request.get(`/api/me/moderation-decisions/${first.id}`)).status()).toBe(
    404,
  );
  await page.goto('/activity');
  await page.getByRole('button', { name: 'Moderation', exact: true }).click();
  await expect(firstCard).toHaveClass(/unread/);
  await firstCard.getByRole('button', { name: 'Mark as read', exact: true }).click();
  await expect(firstCard).not.toHaveClass(/unread/);
  await page.reload();
  await page.getByRole('button', { name: 'Moderation', exact: true }).click();
  await expect(firstCard).not.toHaveClass(/unread/);
  await page.goto(`/posts/${post.id}`);
  await page.getByLabel('Post options').click();
  await page.getByRole('button', { name: 'Edit post', exact: true }).click();
  const edit = page.getByRole('dialog', { name: 'Edit your post' });
  const revised = `Revised publication approval source ${Date.now()}`;
  await edit.getByRole('textbox').fill(revised);
  await edit.getByRole('button', { name: 'Submit for review', exact: true }).click();
  await expect(
    page.getByText('Your edit is awaiting review. The approved version remains public.'),
  ).toBeVisible();
  await expect(page.locator('.post-body')).toContainText(marker);
  await staff.page.reload();
  review = staff.page.getByTestId('review-card').filter({ hasText: revised });
  await review.getByLabel('Review reason').fill('PRIVATE SECOND APPROVAL NOTE');
  await review.getByRole('button', { name: 'Approve & publish', exact: true }).click();
  await expect(review).toHaveCount(0);
  const second = await authorApproval(page, post.id, 2);
  expect(second.id).not.toBe(first.id);
  await privateReviewNotice(page, second.id);
  await page.goto(`/account/moderation-decisions/${first.id}`);
  await expect(exact).toContainText('REVISION 1');
  await page.goto(`/account/moderation-decisions/${second.id}`);
  const latest = page.getByTestId(`moderation-decision-${second.id}`);
  await expect(latest).toContainText('REVISION 2');
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await page.screenshot({ path: 'test-results/publication-approval-mobile-light.png' });
  await page.getByRole('button', { name: 'Use dark theme', exact: true }).click();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await page.screenshot({ path: 'test-results/publication-approval-mobile-dark.png' });
  await latest.getByRole('link', { name: 'Open current thread', exact: true }).click();
  await expect(page.locator('.post-body')).toContainText(revised);
  await page.getByLabel('Post options').click();
  await page.getByRole('button', { name: 'Delete post', exact: true }).click();
  await page
    .getByRole('dialog', { name: 'Delete this post?' })
    .getByRole('button', { name: 'Delete post', exact: true })
    .click();
  await expect(page.getByText('This post was deleted.')).toBeVisible();
  await page.goto(`/account/moderation-decisions/${second.id}`);
  await expect(latest).toContainText('Approved for publication');
  await page.goto('/activity');
  await page.getByRole('button', { name: 'Moderation', exact: true }).click();
  await expect(firstCard).not.toHaveClass(/unread/);
  await expect(reviewActivityCard(page, 'moderation-decisions', second.id)).toBeVisible();
  await other.context.close();
  await staff.context.close();
});

test('reply authors receive private approvals separately from conversation alerts and consent', async ({
  page,
  browser,
}) => {
  test.setTimeout(75_000);
  await page.goto('/');
  await signIn(page, 'Rohan Mehta');
  const restoreFixtureConsent = async () => {
    const response = await page.request.get('/api/me/notification-preferences');
    expect(response.ok()).toBe(true);
    const preference = (await response.json()) as Schema['NotificationPreference'];
    if (!preference.inApp) {
      const updated = await page.request.patch('/api/me/notification-preferences', {
        headers: { 'x-jansetu-csrf': '1', 'if-match': `"${preference.version}"` },
        data: { inApp: true },
      });
      expect(updated.ok()).toBe(true);
    }
  };
  await restoreFixtureConsent();
  const author = await staffPage(browser, 'Ananya Rao');
  const staff = await staffPage(browser, 'Kiran Shah');
  try {
    const post = await publishedContentReportFixture(
      author.page,
      staff.page,
      `Approval reply thread ${Date.now()}`,
    );
    await page.goto(`/posts/${post.id}`);
    const body = `Private reply approval candidate ${Date.now()}`;
    await page.getByLabel('Add to the conversation').fill(body);
    const submitted = page.waitForResponse(
      (r) => r.url().endsWith(`/posts/${post.id}/comments`) && r.request().method() === 'POST',
    );
    await page.getByRole('button', { name: 'Submit comment', exact: true }).click();
    expect((await submitted).status()).toBe(201);
    await expect(page.getByLabel('Add to the conversation')).toHaveValue('');
    await expect(page.locator('.comment').getByText(body, { exact: true })).toBeVisible();
    const comments = (await (
      await page.request.get(`/api/posts/${post.id}/comments`)
    ).json()) as Schema['CommentPage'];
    const reply = comments.items.find((c) => c.candidate?.body === body);
    if (!reply) throw new Error('Missing submitted reply');
    await staff.page.reload();
    const review = staff.page.getByTestId('review-card').filter({ hasText: body });
    await review.getByLabel('Review reason').fill('PRIVATE REPLY APPROVAL NOTE');
    await review.getByRole('button', { name: 'Approve & publish', exact: true }).click();
    await expect(review).toHaveCount(0);
    const decision = await authorApproval(page, reply.id, 1);
    await privateReviewNotice(page, decision.id);
    await expect
      .poll(async () => {
        const list = (await (
          await author.page.request.get('/api/me/activity?filter=SOCIAL')
        ).json()) as Schema['ActivityPage'];
        return list.items.some((n) => n.kind === 'REPLY' && n.target.id === post.id);
      })
      .toBe(true);
    const writerAlerts = (await (
      await page.request.get('/api/me/activity?filter=MODERATION')
    ).json()) as Schema['ActivityPage'];
    expect(writerAlerts.items.find((n) => n.target.id === decision.id)?.kind).toBe(
      'PUBLICATION_APPROVAL',
    );
    const parentAlerts = (await (
      await author.page.request.get('/api/me/activity?filter=MODERATION')
    ).json()) as Schema['ActivityPage'];
    expect(parentAlerts.items.some((n) => n.target.id === decision.id)).toBe(false);
    expect(
      (await author.page.request.get(`/api/me/moderation-decisions/${decision.id}`)).status(),
    ).toBe(404);
    await page.goto('/activity');
    await page.getByRole('button', { name: 'Conversations', exact: true }).click();
    await expect(reviewActivityCard(page, 'moderation-decisions', decision.id)).toHaveCount(0);
    await page.getByRole('button', { name: 'Moderation', exact: true }).click();
    const card = reviewActivityCard(page, 'moderation-decisions', decision.id);
    await expect(card).toContainText('Publication review');
    await card.getByRole('button', { name: 'Mark as read', exact: true }).click();
    await expect(card).not.toHaveClass(/unread/);
    await page.goto('/account#activity-settings');
    await page.getByRole('switch', { name: 'In-app notifications' }).uncheck();
    await page.getByRole('button', { name: 'Save activity preferences', exact: true }).click();
    await expect(page.getByRole('status')).toContainText('In-app notifications paused');
    await page.goto('/activity');
    await expect(
      page.getByText('In-app notifications are paused.', { exact: false }),
    ).toBeVisible();
    await expect(page.getByText('0 unread', { exact: true })).toBeVisible();
    await expect(card).toHaveCount(0);
    await page.goto(`/account/moderation-decisions/${decision.id}`);
    await expect(page.getByTestId(`moderation-decision-${decision.id}`)).toContainText(
      'Approved for publication',
    );
    await page.goto('/account#activity-settings');
    await page.getByRole('switch', { name: 'In-app notifications' }).check();
    await page.getByRole('button', { name: 'Save activity preferences', exact: true }).click();
    await expect(page.getByRole('status')).toContainText('In-app notifications enabled');
    await page.goto('/activity');
    await page.getByRole('button', { name: 'Moderation', exact: true }).click();
    await expect(card).not.toHaveClass(/unread/);
    await card.getByRole('button', { name: 'Mark as unread', exact: true }).click();
    await expect(card).toHaveClass(/unread/);
  } finally {
    try {
      await restoreFixtureConsent();
    } finally {
      await Promise.all([staff.context.close(), author.context.close()]);
    }
  }
});

test('private moderation activity opens exact owner records and preserves appeal history after deletion', async ({
  page,
  browser,
}) => {
  test.setTimeout(90_000);
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  const original = await staffPage(browser, 'Kiran Shah');
  const reviewer = await staffPage(browser, 'Neha Sen');
  const other = await staffPage(browser, 'Rohan Mehta');
  const marker = `Private notice source ${Date.now()}`;
  const { post, decision } = await rejectedAppealFixture(page, original.page, marker);
  await privateReviewNotice(page, decision.id);
  const notices = (await (
    await page.request.get('/api/me/activity?filter=MODERATION')
  ).json()) as Schema['ActivityPage'];
  const notice = notices.items.find((n) => n.target.id === decision.id);
  expect(notice?.kind).toBe('MODERATION_DECISION');
  expect(notice?.actor).toBeNull();
  for (const secret of [marker, 'PRIVATE ORIGINAL APPEAL REVIEW NOTE', decision.reason])
    expect(JSON.stringify(notices)).not.toContain(secret);
  await page.goto('/activity');
  await page.getByRole('button', { name: 'Moderation', exact: true }).click();
  let card = reviewActivityCard(page, 'moderation-decisions', decision.id);
  await expect(card).toContainText('Private moderation');
  await expect(card).toContainText('A moderation decision is available');
  await card.getByRole('button', { name: 'Mark as read', exact: true }).click();
  await expect(card).not.toHaveClass(/unread/);
  await page.reload();
  await page.getByRole('button', { name: 'Moderation', exact: true }).click();
  await expect(card).not.toHaveClass(/unread/);
  await card.locator('.activity-target').click();
  await expect(page).toHaveURL(`/account/moderation-decisions/${decision.id}`);
  await expect(
    page.getByRole('heading', { name: 'Moderation decision', exact: true }),
  ).toBeVisible();
  const privateDecision = page.getByTestId(`moderation-decision-${decision.id}`);
  await expect(privateDecision).toContainText(decision.reason);
  await expect(privateDecision).not.toContainText(marker);
  await other.page.goto(`/account/moderation-decisions/${decision.id}`);
  await expect(
    other.page.getByRole('heading', { name: 'This private record is unavailable' }),
  ).toBeVisible();
  await expect(other.page.locator('main')).not.toContainText(decision.reason);
  expect(
    (await original.page.request.get(`/api/me/moderation-decisions/${decision.id}`)).status(),
  ).toBe(404);
  await privateDecision.getByRole('button', { name: 'Appeal decision', exact: true }).click();
  const form = page.getByRole('dialog', { name: 'Appeal this decision' });
  await form
    .getByLabel('Appeal grounds')
    .fill('Please review this fictional restriction independently.');
  await form.getByRole('button', { name: 'Submit appeal', exact: true }).click();
  await expect(form).toHaveCount(0);
  const owned = (await (await page.request.get('/api/me/appeals')).json()) as Schema['AppealPage'];
  const appeal = owned.items.find((v) => v.decisionId === decision.id);
  if (!appeal) throw new Error('Missing exact-record appeal');
  await reviewer.page.getByRole('button', { name: 'Appeals', exact: true }).click();
  const review = reviewer.page.getByTestId(`appeal-review-${appeal.id}`);
  await review.getByRole('button', { name: 'Take review', exact: true }).click();
  await review
    .getByLabel('Reason shared with author')
    .fill('Independent review upholds this fictional publication decision.');
  await review.getByRole('button', { name: 'Uphold decision', exact: true }).click();
  await reviewer.page
    .getByRole('dialog', { name: 'Uphold this decision?' })
    .getByRole('button', { name: 'Confirm appeal outcome', exact: true })
    .click();
  await expect(review).toHaveCount(0);
  await privateReviewNotice(page, appeal.id);
  const current = (await (
    await page.request.get(`/api/posts/${post.id}`)
  ).json()) as Schema['Post'];
  expect(
    (
      await page.request.delete(`/api/posts/${post.id}`, {
        headers: { 'x-jansetu-csrf': '1', 'if-match': `"${current.version}"` },
      })
    ).status(),
  ).toBe(204);
  await page.goto('/activity');
  await page.getByRole('button', { name: 'Moderation', exact: true }).click();
  card = reviewActivityCard(page, 'appeals', appeal.id);
  await expect(card).toContainText('An independent decision is available');
  await expect(reviewActivityCard(page, 'moderation-decisions', decision.id)).toBeVisible();
  await page.setViewportSize({ width: 320, height: 740 });
  await card.evaluate((el) =>
    window.scrollTo({
      top: el.getBoundingClientRect().top + window.scrollY - 150,
      behavior: 'instant',
    }),
  );
  await expect(card).toBeInViewport();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await page.screenshot({ path: 'test-results/private-moderation-activity-mobile.png' });
  await card.locator('.activity-target').click();
  await expect(page).toHaveURL(`/account/appeals/${appeal.id}`);
  const outcome = page.getByTestId(`appeal-${appeal.id}`);
  await expect(outcome).toContainText('Decision upheld');
  await expect(outcome).not.toContainText(marker);
  await expect(outcome).not.toContainText('PRIVATE ORIGINAL APPEAL REVIEW NOTE');
  await page.getByRole('button', { name: 'Use dark theme', exact: true }).click();
  await outcome.evaluate((el) =>
    window.scrollTo({
      top: el.getBoundingClientRect().top + window.scrollY - 150,
      behavior: 'instant',
    }),
  );
  await expect(outcome).toBeInViewport();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await page.screenshot({ path: 'test-results/private-appeal-record-mobile-dark.png' });
  await other.page.goto(`/account/appeals/${appeal.id}`);
  await expect(
    other.page.getByRole('heading', { name: 'This private record is unavailable' }),
  ).toBeVisible();
  await expect(other.page.locator('main')).not.toContainText('Independent review upholds');
  await original.context.close();
  await reviewer.context.close();
  await other.context.close();
});
test('private moderation alerts honor in-app consent while exact records stay available', async ({
  page,
  browser,
}) => {
  test.setTimeout(75_000);
  await page.goto('/');
  await signIn(page, 'Rohan Mehta');
  const original = await staffPage(browser, 'Kiran Shah');
  const marker = `Private consent notice ${Date.now()}`;
  const { decision } = await rejectedAppealFixture(page, original.page, marker);
  await privateReviewNotice(page, decision.id);
  await page.goto('/activity');
  await page.getByRole('button', { name: 'Moderation', exact: true }).click();
  const card = reviewActivityCard(page, 'moderation-decisions', decision.id);
  await expect(card).toBeVisible();
  await card.getByRole('button', { name: 'Mark as read', exact: true }).click();
  await expect(card).not.toHaveClass(/unread/);
  await page.goto('/account#activity-settings');
  await page.getByRole('switch', { name: 'In-app notifications' }).uncheck();
  await page.getByRole('button', { name: 'Save activity preferences', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('In-app notifications paused');
  await page.goto('/activity');
  await page.getByRole('button', { name: 'Moderation', exact: true }).click();
  await expect(page.getByText('In-app notifications are paused.', { exact: false })).toBeVisible();
  await expect(card).toHaveCount(0);
  expect((await (await page.request.get('/api/me/activity/summary')).json()).unreadCount).toBe(0);
  await page.goto(`/account/moderation-decisions/${decision.id}`);
  await expect(page.getByTestId(`moderation-decision-${decision.id}`)).toContainText(
    decision.reason,
  );
  await page.goto('/account#activity-settings');
  await page.getByRole('switch', { name: 'In-app notifications' }).check();
  await page.getByRole('button', { name: 'Save activity preferences', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('In-app notifications enabled');
  await page.goto('/activity');
  await page.getByRole('button', { name: 'Moderation', exact: true }).click();
  await expect(card).toBeVisible();
  await expect(card).not.toHaveClass(/unread/);
  await card.getByRole('button', { name: 'Mark as unread', exact: true }).click();
  await expect(card).toHaveClass(/unread/);
  await original.context.close();
});

test('reporter activity opens a private dismissed receipt and retains it after source deletion', async ({
  page,
  browser,
}) => {
  test.setTimeout(90_000);
  await page.goto('/');
  await signIn(page, 'Rohan Mehta');
  const author = await staffPage(browser, 'Ananya Rao');
  const mod = await staffPage(browser, 'Kiran Shah');
  const marker = `Reported activity source ${Date.now()}`;
  const details = `PRIVATE REPORTER DETAILS ${Date.now()}`;
  const explanation = 'This fictional report was dismissed after an independent policy review.';
  const post = await publishedContentReportFixture(author.page, mod.page, marker);
  await page.goto(`/posts/${post.id}`);
  await page.getByLabel('Post options').click();
  await page.getByRole('button', { name: 'Report post', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Report this post?' });
  await dialog.getByLabel(/Additional detail/).fill(details);
  await dialog.getByRole('button', { name: 'Submit content report', exact: true }).click();
  await page
    .getByRole('dialog', { name: 'Content report received' })
    .getByRole('button', { name: 'Done', exact: true })
    .click();
  const receipt = await ownContentReportFor(page, post.id);
  await mod.page.goto('/studio');
  await mod.page.getByRole('button', { name: 'Reported content', exact: true }).click();
  const review = mod.page.getByTestId(`content-report-review-${receipt.id}`);
  await review.getByLabel('Decision reason').fill(explanation);
  await review.getByRole('button', { name: 'Dismiss report', exact: true }).click();
  await expect(review).toHaveCount(0);
  await privateReviewNotice(page, receipt.id);
  const notices = (await (
    await page.request.get('/api/me/activity?filter=MODERATION')
  ).json()) as Schema['ActivityPage'];
  const notice = notices.items.find((item) => item.target.id === receipt.id);
  expect(notice?.kind).toBe('CONTENT_REPORT_OUTCOME');
  expect(notice?.target.kind).toBe('CONTENT_REPORT');
  expect(notice?.actor).toBeNull();
  for (const secret of [details, explanation, marker, post.id])
    expect(JSON.stringify(notice)).not.toContain(secret);
  await page.goto('/activity');
  await page.getByRole('button', { name: 'Moderation', exact: true }).click();
  const alert = reviewActivityCard(page, 'content-reports', receipt.id);
  await expect(alert).toContainText('Private content report');
  await expect(alert).toContainText('A review outcome is available');
  await alert.getByRole('button', { name: 'Mark as read', exact: true }).click();
  await expect(alert).not.toHaveClass(/unread/);
  await page.reload();
  await page.getByRole('button', { name: 'Moderation', exact: true }).click();
  await expect(alert).not.toHaveClass(/unread/);
  await alert.locator('.activity-target').click();
  await expect(page).toHaveURL(`/account/content-reports/${receipt.id}`);
  const record = page.getByTestId(`content-report-${receipt.id}`);
  await expect(record).toContainText(details);
  await expect(record).toContainText(explanation);
  await expect(record).toContainText(marker);
  await author.page.goto(`/account/content-reports/${receipt.id}`);
  await expect(
    author.page.getByRole('heading', { name: 'This private record is unavailable' }),
  ).toBeVisible();
  await expect(author.page.locator('main')).not.toContainText(details);
  expect((await mod.page.request.get(`/api/me/content-reports/${receipt.id}`)).status()).toBe(404);
  expect(
    (
      await author.page.request.delete(`/api/posts/${post.id}`, {
        headers: { 'x-jansetu-csrf': '1', 'if-match': `"${post.version}"` },
      })
    ).status(),
  ).toBe(204);
  await page.getByRole('button', { name: 'Refresh record', exact: true }).click();
  await expect(record).toContainText('This content is unavailable');
  await expect(record).not.toContainText(marker);
  await expect(record).toContainText(explanation);
  await page.setViewportSize({ width: 320, height: 740 });
  await record.evaluate((el) =>
    window.scrollTo({
      top: el.getBoundingClientRect().top + window.scrollY - 150,
      behavior: 'instant',
    }),
  );
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await page.screenshot({ path: 'test-results/content-report-outcome-mobile-light.png' });
  await page.getByRole('button', { name: 'Use dark theme', exact: true }).click();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await page.screenshot({ path: 'test-results/content-report-outcome-mobile-dark.png' });
  await page.goto('/activity');
  await page.getByRole('button', { name: 'Moderation', exact: true }).click();
  await expect(alert).toBeVisible();
  await expect(alert).not.toHaveClass(/unread/);
  await page.goto('/account');
  await page.getByRole('button', { name: 'Sign out here', exact: true }).click();
  await page.goto(`/account/content-reports/${receipt.id}`);
  await expect(
    page.getByRole('heading', { name: 'Private review record', exact: true }),
  ).toBeVisible();
  await expect(page.locator('main')).not.toContainText(details);
  await signIn(page, 'Ananya Rao');
  await page.goto(`/account/content-reports/${receipt.id}`);
  await expect(
    page.getByRole('heading', { name: 'This private record is unavailable' }),
  ).toBeVisible();
  await expect(page.locator('main')).not.toContainText(details);
  await author.context.close();
  await mod.context.close();
});

test('report removal alerts separate reporter and author records and honor notification consent', async ({
  page,
  browser,
}) => {
  test.setTimeout(90_000);
  await page.goto('/');
  await signIn(page, 'Rohan Mehta');
  const author = await staffPage(browser, 'Ananya Rao');
  const mod = await staffPage(browser, 'Kiran Shah');
  const marker = `Removed reporter activity ${Date.now()}`;
  const details = `PRIVATE REMOVAL REPORT DETAILS ${Date.now()}`;
  const reporterReason =
    'PRIVATE REPORTER OUTCOME: fictional content was removed under community policy.';
  const authorReason = 'This fictional revision does not meet the publication rule.';
  const post = await publishedContentReportFixture(author.page, mod.page, marker);
  const response = await page.request.post('/api/content-reports', {
    headers: { 'x-jansetu-csrf': '1', 'idempotency-key': crypto.randomUUID() },
    data: {
      targetType: 'POST',
      targetId: post.id,
      targetRevision: 1,
      reasonCode: 'PRIVACY',
      details,
    },
  });
  expect(response.status()).toBe(201);
  const receipt = (await response.json()) as Schema['ContentReportReceipt'];
  await mod.page.goto('/studio');
  await mod.page.getByRole('button', { name: 'Reported content', exact: true }).click();
  const review = mod.page.getByTestId(`content-report-review-${receipt.id}`);
  await review.getByLabel('Decision reason').fill(reporterReason);
  await review.getByLabel('Reason shared with author').fill(authorReason);
  await review.getByRole('button', { name: 'Remove reported content', exact: true }).click();
  await mod.page
    .getByRole('dialog', { name: 'Remove reported content?' })
    .getByRole('button', { name: 'Confirm removal', exact: true })
    .click();
  await expect(review).toHaveCount(0);
  await privateReviewNotice(page, receipt.id);
  const authorHistory = (await (
    await author.page.request.get('/api/me/moderation-decisions')
  ).json()) as Schema['AuthorModerationDecisionPage'];
  const authorDecision = authorHistory.items.find(
    (decision) => decision.target.id === post.id && decision.action === 'REMOVE',
  );
  if (!authorDecision) throw new Error('Missing separate author removal record');
  await privateReviewNotice(author.page, authorDecision.id);
  const own = (await (
    await page.request.get('/api/me/activity?filter=MODERATION')
  ).json()) as Schema['ActivityPage'];
  const theirs = (await (
    await author.page.request.get('/api/me/activity?filter=MODERATION')
  ).json()) as Schema['ActivityPage'];
  expect(own.items.some((item) => item.target.id === authorDecision.id)).toBe(false);
  expect(theirs.items.some((item) => item.target.id === receipt.id)).toBe(false);
  expect((await author.page.request.get(`/api/me/content-reports/${receipt.id}`)).status()).toBe(
    404,
  );
  await author.page.goto(`/account/moderation-decisions/${authorDecision.id}`);
  const authorRecord = author.page.getByTestId(`moderation-decision-${authorDecision.id}`);
  await expect(authorRecord).toContainText(authorReason);
  await expect(authorRecord).not.toContainText(reporterReason);
  await expect(authorRecord).not.toContainText(details);
  await page.goto('/activity');
  await page.getByRole('button', { name: 'Moderation', exact: true }).click();
  const alert = reviewActivityCard(page, 'content-reports', receipt.id);
  await alert.getByRole('button', { name: 'Mark as read', exact: true }).click();
  await expect(alert).not.toHaveClass(/unread/);
  await page.goto('/account#activity-settings');
  await page.getByRole('switch', { name: 'In-app notifications' }).uncheck();
  await page.getByRole('button', { name: 'Save activity preferences', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('In-app notifications paused');
  await page.goto('/activity');
  await page.getByRole('button', { name: 'Moderation', exact: true }).click();
  await expect(alert).toHaveCount(0);
  expect((await (await page.request.get('/api/me/activity/summary')).json()).unreadCount).toBe(0);
  await page.goto(`/account/content-reports/${receipt.id}`);
  const record = page.getByTestId(`content-report-${receipt.id}`);
  await expect(record).toContainText('Content removed');
  await expect(record).toContainText(details);
  await expect(record).toContainText(reporterReason);
  await expect(record).not.toContainText(marker);
  await page.goto('/account#activity-settings');
  await page.getByRole('switch', { name: 'In-app notifications' }).check();
  await page.getByRole('button', { name: 'Save activity preferences', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('In-app notifications enabled');
  await page.goto('/activity');
  await page.getByRole('button', { name: 'Moderation', exact: true }).click();
  await expect(alert).toBeVisible();
  await expect(alert).not.toHaveClass(/unread/);
  await alert.getByRole('button', { name: 'Mark as unread', exact: true }).click();
  await expect(alert).toHaveClass(/unread/);
  await author.context.close();
  await mod.context.close();
});

async function publishedSharingFixture(owner: Page, publisher: Page) {
  const fixture = await publicProgressFixture(owner, publisher);
  const title = `Resident sharing crossing ${Date.now()} ${crypto.randomUUID()}`;
  const fields = {
    title,
    summary: 'A fictional restoration task awaits agency work.',
    area: 'Synthetic broad area',
    reason: 'PRIVATE INITIAL SHARING REVIEW',
    reviewed: true,
    publicationVersion: 0,
  };
  const response = await publisher.request.post(
    `/api/authority/cases/${fixture.caseId}/publications`,
    {
      headers: { 'x-jansetu-csrf': '1', 'if-match': '"1"' },
      data: fields,
    },
  );
  expect(response.status()).toBe(200);
  const publication = (await response.json()) as Schema['PublicationResult'];
  return { ...fixture, fields, publication };
}
async function openResidentSharing(owner: Page, statement: string) {
  await owner.goto('/my-reports');
  const report = owner.locator('.my-report').filter({ hasText: statement });
  await report.getByRole('button', { name: 'Manage public sharing', exact: true }).click();
  await expect(owner.getByTestId('resident-public-preview')).toBeVisible();
}
async function openSharingReview(publisher: Page, requestId: string) {
  await publisher.goto('/studio');
  await publisher.getByRole('button', { name: 'Public sharing', exact: true }).click();
  await publisher.getByTestId(`withdrawal-review-${requestId}`).click();
  await expect(publisher.getByTestId('publisher-withdrawal-review')).toBeVisible();
}

async function approvedRenewalFixture(owner: Page, publisher: Page) {
  const fixture = await publishedSharingFixture(owner, publisher);
  const response = await owner.request.post(
    `/api/my-reports/${fixture.report.id}/publication-withdrawal-requests`,
    {
      headers: { 'x-jansetu-csrf': '1', 'idempotency-key': crypto.randomUUID() },
      data: {
        clientRequestId: crypto.randomUUID(),
        publicationVersion: 1,
        reasonCode: 'PRIVACY',
        confirmed: true,
      },
    },
  );
  expect(response.status()).toBe(201);
  const request = (await response.json()) as Schema['PublicationWithdrawalRequest'];
  const approved = await publisher.request.post(
    `/api/authority/publication-withdrawal-requests/${request.id}/decisions`,
    {
      headers: { 'x-jansetu-csrf': '1', 'if-match': '"1"' },
      data: {
        result: 'APPROVED',
        caseVersion: 1,
        publicationVersion: 1,
        internalReason: 'PRIVATE RENEWAL INVESTIGATION',
        residentReason: 'Public progress withdrawn at your request.',
        reviewed: true,
      },
    },
  );
  expect(approved.status()).toBe(200);
  await owner.goto('/my-reports');
  await owner
    .locator('.my-report')
    .filter({ hasText: fixture.statement })
    .getByRole('button', { name: 'Manage public sharing', exact: true })
    .click();
  await expect(owner.getByTestId('resident-sharing-permission')).toBeVisible();
  return { ...fixture, request };
}

test('resident permission renewal stays hidden until fresh review and each withdrawal needs fresh consent', async ({
  page,
  browser,
}) => {
  test.setTimeout(120_000);
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  const staff = await staffPage(browser, 'Kiran Shah');
  try {
    const { caseId, report, statement, request, publication } = await approvedRenewalFixture(
      page,
      staff.page,
    );
    const sharing = page.getByTestId('resident-public-sharing');
    const permission = sharing.getByTestId('resident-sharing-permission');
    const form = sharing.getByTestId('sharing-permission-confirmation');
    const allow = 'Allow future publisher review';
    const confirm = 'Confirm future publisher review';
    await permission.getByRole('button', { name: allow, exact: true }).click();
    await expect(form.getByRole('button', { name: confirm })).toBeDisabled();
    await expect(form).toContainText(
      'Progress stays hidden until a fresh publisher review succeeds.',
    );
    await form.getByRole('button', { name: 'Keep current permission' }).click();
    await expect(permission.getByRole('button', { name: allow, exact: true })).toBeVisible();
    await page.setViewportSize({ width: 320, height: 820 });
    await permission.getByRole('button', { name: allow, exact: true }).click();
    await form
      .getByLabel('I allow future sanitized public updates after publisher review.')
      .check();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await page.screenshot({ path: 'test-results/resident-renewal-mobile-light.png' });
    await form.getByRole('button', { name: 'Keep current permission' }).click();
    await page.getByRole('button', { name: 'Close dialog' }).click();
    await page.getByRole('button', { name: 'Use dark theme', exact: true }).click();
    await page
      .locator('.my-report')
      .filter({ hasText: statement })
      .getByRole('button', { name: 'Manage public sharing' })
      .click();
    await permission.getByRole('button', { name: allow, exact: true }).click();
    await expect(
      form.getByLabel('I allow future sanitized public updates after publisher review.'),
    ).not.toBeChecked();
    await form
      .getByLabel('I allow future sanitized public updates after publisher review.')
      .check();
    await page.screenshot({ path: 'test-results/resident-renewal-mobile-dark.png' });
    await form.getByRole('button', { name: confirm }).click();
    await expect(permission).toContainText('You have allowed future publisher review.');
    await expect(sharing.getByTestId('resident-public-preview')).toHaveCount(0);
    expect((await page.request.get(`/api/case-receipts/${publication.receiptId}`)).status()).toBe(
      404,
    );
    await expect(sharing).not.toContainText('PRIVATE RENEWAL INVESTIGATION');
    await expect(sharing).not.toContainText(caseId);
    await permission.getByRole('button', { name: 'Undo sharing permission', exact: true }).click();
    await expect(form.getByRole('button', { name: 'Confirm undo permission' })).toBeDisabled();
    await form.getByRole('button', { name: 'Keep current permission' }).click();
    await permission.getByRole('button', { name: 'Undo sharing permission', exact: true }).click();
    await form.getByLabel('I want to undo this sharing permission.').check();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await page.screenshot({ path: 'test-results/resident-renewal-undo-mobile-dark.png' });
    await form.getByRole('button', { name: 'Confirm undo permission' }).click();
    await expect(permission).toContainText('Permission undone');
    await expect(permission.getByRole('button', { name: allow, exact: true })).toBeVisible();
    const blocked = await staff.page.request.post(`/api/authority/cases/${caseId}/publications`, {
      headers: { 'x-jansetu-csrf': '1', 'if-match': '"1"' },
      data: {
        title: 'Permission undone',
        summary: 'Fictional safe summary',
        area: 'Broad fictional area',
        reason: 'Private fresh review',
        publicationVersion: 2,
        reviewed: true,
      },
    });
    expect(blocked.status()).toBe(403);
    await permission.getByRole('button', { name: allow, exact: true }).click();
    await form
      .getByLabel('I allow future sanitized public updates after publisher review.')
      .check();
    await form.getByRole('button', { name: confirm }).click();
    await expect(permission.getByRole('button', { name: 'Undo sharing permission' })).toBeVisible();
    await openPublicationReview(staff.page, caseId);
    const review = staff.page.getByTestId('publication-review');
    await expect(
      review.getByLabel('I reviewed this public preview for identifying details.'),
    ).not.toBeChecked();
    await review
      .getByLabel('Public title', { exact: true })
      .fill('Fresh reviewed progress after resident opt-in');
    await review
      .getByRole('textbox', { name: 'Private publication reason', exact: true })
      .fill('PRIVATE FRESH RESIDENT OPT-IN REVIEW');
    await review.getByLabel('I reviewed this public preview for identifying details.').check();
    const fresh = await savePublicProgress(staff.page, 'Republish reviewed progress');
    expect(fresh.receiptId).toBe(publication.receiptId);
    expect(fresh.publicationVersion).toBe(3);
    expect(fresh.version).toBe(1);
    await sharing.getByRole('button', { name: 'Refresh sharing review' }).click();
    await expect(sharing.getByTestId('resident-public-preview')).toContainText('REVISION 3');
    await expect(permission.getByRole('button', { name: 'Undo sharing permission' })).toHaveCount(
      0,
    );
    await sharing.getByRole('button', { name: 'Request withdrawal', exact: true }).click();
    await sharing
      .getByLabel('I reviewed this public progress and want to request withdrawal.')
      .check();
    const requested = page.waitForResponse(
      (r) =>
        r.url().endsWith('/publication-withdrawal-requests') && r.request().method() === 'POST',
    );
    await sharing.getByRole('button', { name: 'Submit withdrawal request' }).click();
    const response = await requested;
    expect(response.status()).toBe(201);
    const next = (await response.json()) as Schema['PublicationWithdrawalRequest'];
    expect(next.id).not.toBe(request.id);
    const approved = await staff.page.request.post(
      `/api/authority/publication-withdrawal-requests/${next.id}/decisions`,
      {
        headers: { 'x-jansetu-csrf': '1', 'if-match': '"1"' },
        data: {
          result: 'APPROVED',
          caseVersion: 1,
          publicationVersion: 3,
          internalReason: 'Private second review',
          residentReason: 'Second withdrawal approved.',
          reviewed: true,
        },
      },
    );
    expect(approved.status()).toBe(200);
    await sharing.getByRole('button', { name: 'Refresh sharing review' }).click();
    await expect(permission.getByRole('button', { name: allow, exact: true })).toBeVisible();
    await expect(permission).not.toContainText('You have allowed future publisher review.');
    await expect(sharing.getByTestId(`owned-withdrawal-${request.id}`)).toContainText('approved');
    const current = (await (
      await page.request.get(`/api/my-reports/${report.id}/public-sharing`)
    ).json()) as Schema['OwnerPublicSharing'];
    expect(current.permissionRequest?.id).toBe(next.id);
    expect(current.permissionRequest?.sharingReview?.renewal).toBeNull();
    expect(current.publication).toBeNull();
  } finally {
    await staff.context.close();
  }
});

test('resident permission lost acknowledgements and stale undo recover current controls', async ({
  page,
  browser,
}) => {
  test.setTimeout(120_000);
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  const staff = await staffPage(browser, 'Kiran Shah');
  try {
    const { caseId, report, request, fields, publication } = await approvedRenewalFixture(
      page,
      staff.page,
    );
    const sharing = page.getByTestId('resident-public-sharing');
    const permission = sharing.getByTestId('resident-sharing-permission');
    const form = sharing.getByTestId('sharing-permission-confirmation');
    const root = `**/api/my-reports/${report.id}/publication-withdrawal-requests/${request.id}/sharing-renewals`;
    let first: Schema['PublicationWithdrawalRequest'] | undefined;
    let body: Schema['PublicationSharingRenewalInput'] | undefined;
    let key: string | undefined;
    await page.route(root, async (route) => {
      const input = route.request().postDataJSON() as Schema['PublicationSharingRenewalInput'];
      const commandKey = route.request().headers()['idempotency-key'];
      const response = await route.fetch();
      expect(response.status()).toBe(201);
      const record = (await response.json()) as Schema['PublicationWithdrawalRequest'];
      if (!first) {
        first = record;
        body = input;
        key = commandKey;
        await route.abort('failed');
      } else {
        expect(input).toEqual(body);
        expect(commandKey).toBe(key);
        expect(record.sharingReview?.renewal?.id).toBe(first.sharingReview?.renewal?.id);
        await route.fulfill({ response });
      }
    });
    await permission.getByRole('button', { name: 'Allow future publisher review' }).click();
    await form
      .getByLabel('I allow future sanitized public updates after publisher review.')
      .check();
    await form.getByRole('button', { name: 'Confirm future publisher review' }).click();
    await expect(form.locator('.form-error')).toContainText(/fetch/i);
    await form.getByRole('button', { name: 'Confirm future publisher review' }).click();
    await expect(permission.getByRole('button', { name: 'Undo sharing permission' })).toBeVisible();
    await page.unroute(root);
    expect((await page.request.get(`/api/case-receipts/${publication.receiptId}`)).status()).toBe(
      404,
    );
    const renewal = first?.sharingReview?.renewal;
    expect(renewal).toBeTruthy();
    const undoRoute = `${root}/${renewal!.id}/cancellations`;
    await page.route(undoRoute, async (route) => {
      const response = await route.fetch();
      expect(response.status()).toBe(200);
      await route.abort('failed');
    });
    await permission.getByRole('button', { name: 'Undo sharing permission' }).click();
    await form.getByLabel('I want to undo this sharing permission.').check();
    await form.getByRole('button', { name: 'Confirm undo permission' }).click();
    await expect(form.locator('.form-error')).toContainText(/fetch/i);
    await form.getByRole('button', { name: 'Refresh sharing review' }).click();
    await expect(form).toHaveCount(0);
    await expect(permission).toContainText('Permission undone');
    await page.unroute(undoRoute);
    await permission.getByRole('button', { name: 'Allow future publisher review' }).click();
    await form
      .getByLabel('I allow future sanitized public updates after publisher review.')
      .check();
    await form.getByRole('button', { name: 'Confirm future publisher review' }).click();
    await expect(permission.getByRole('button', { name: 'Undo sharing permission' })).toBeVisible();
    await permission.getByRole('button', { name: 'Undo sharing permission' }).click();
    await form.getByLabel('I want to undo this sharing permission.').check();
    const published = await staff.page.request.post(`/api/authority/cases/${caseId}/publications`, {
      headers: { 'x-jansetu-csrf': '1', 'if-match': '"1"' },
      data: {
        ...fields,
        title: 'Current reviewed publication after opt-in',
        publicationVersion: 2,
      },
    });
    expect(published.status()).toBe(200);
    const stale = page.waitForResponse(
      (r) => r.url().endsWith('/cancellations') && r.request().method() === 'POST',
    );
    await form.getByRole('button', { name: 'Confirm undo permission' }).click();
    expect((await stale).status()).toBe(409);
    await expect(form).toContainText('Refresh the sharing review and confirm your choice again.');
    await expect(form.getByRole('button', { name: 'Confirm undo permission' })).toBeDisabled();
    await form.getByRole('button', { name: 'Refresh sharing review' }).click();
    await expect(form).toHaveCount(0);
    await expect(sharing.getByTestId('resident-public-preview')).toContainText('REVISION 3');
    await expect(
      sharing.getByRole('button', { name: 'Request withdrawal', exact: true }),
    ).toBeVisible();
    await expect(permission.getByRole('button', { name: 'Undo sharing permission' })).toHaveCount(
      0,
    );
    const current = (await (
      await page.request.get(`/api/my-reports/${report.id}/public-sharing`)
    ).json()) as Schema['OwnerPublicSharing'];
    expect(current.permissionRequest?.sharingReview?.renewal?.state).toBe('ACTIVE');
    expect(current.permissionRequest?.sharingReview?.renewal?.id).not.toBe(renewal!.id);
  } finally {
    await staff.context.close();
  }
});

test('resident sharing cancellation and reviewed approval preserve agency work', async ({
  page,
  browser,
}) => {
  test.setTimeout(120_000);
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  const staff = await staffPage(browser, 'Kiran Shah');
  const officer = await staffPage(browser, 'City Works team');
  try {
    const { caseId, report, statement, publication } = await publishedSharingFixture(
      page,
      staff.page,
    );
    await page.setViewportSize({ width: 320, height: 820 });
    await openResidentSharing(page, statement);
    const sharing = page.getByTestId('resident-public-sharing');
    await sharing.getByRole('button', { name: 'Request withdrawal', exact: true }).click();
    await expect(sharing.getByRole('button', { name: 'Submit withdrawal request' })).toBeDisabled();
    await sharing.getByLabel('Reason for withdrawal').selectOption('LOCATION');
    await sharing
      .getByLabel('I reviewed this public progress and want to request withdrawal.')
      .check();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await page.screenshot({ path: 'test-results/resident-sharing-mobile-light.png' });
    const requested = page.waitForResponse(
      (r) =>
        r.url().endsWith('/publication-withdrawal-requests') && r.request().method() === 'POST',
    );
    await sharing.getByRole('button', { name: 'Submit withdrawal request' }).click();
    const response = await requested;
    expect(response.status()).toBe(201);
    const first = (await response.json()) as Schema['PublicationWithdrawalRequest'];
    const firstCard = sharing.getByTestId(`owned-withdrawal-${first.id}`);
    await expect(firstCard).toContainText('requested');
    await firstCard.getByRole('button', { name: 'Cancel request' }).click();
    await expect(page.getByRole('dialog', { name: 'Cancel withdrawal request?' })).toBeVisible();
    await sharing.getByRole('button', { name: 'Keep request' }).click();
    await expect(firstCard).toContainText('requested');
    await firstCard.getByRole('button', { name: 'Cancel request' }).click();
    await sharing.getByLabel('I want to cancel this pending request.').check();
    await sharing.getByRole('button', { name: 'Confirm cancellation' }).click();
    await expect(firstCard).toContainText('cancelled');
    expect((await page.request.get(`/api/case-receipts/${publication.receiptId}`)).status()).toBe(
      200,
    );
    await page.getByRole('button', { name: 'Close dialog' }).click();
    await page.getByRole('button', { name: 'Use dark theme', exact: true }).click();
    await page
      .locator('.my-report')
      .filter({ hasText: statement })
      .getByRole('button', { name: 'Manage public sharing' })
      .click();
    await sharing.getByRole('button', { name: 'Request withdrawal', exact: true }).click();
    await sharing
      .getByLabel('I reviewed this public progress and want to request withdrawal.')
      .check();
    await page.screenshot({ path: 'test-results/resident-sharing-mobile-dark.png' });
    const requestAgain = page.waitForResponse(
      (r) =>
        r.url().endsWith('/publication-withdrawal-requests') && r.request().method() === 'POST',
    );
    await sharing.getByRole('button', { name: 'Submit withdrawal request' }).click();
    const secondResponse = await requestAgain;
    expect(secondResponse.status()).toBe(201);
    const second = (await secondResponse.json()) as Schema['PublicationWithdrawalRequest'];
    expect(second.id).not.toBe(first.id);
    await expect(sharing.getByTestId(`owned-withdrawal-${second.id}`)).toContainText('requested');
    await openPublicationReview(staff.page, caseId);
    await expect(staff.page.getByTestId('publication-review')).toContainText(
      'Public sharing is paused',
    );
    await expect(
      staff.page.getByRole('button', { name: 'Save reviewed correction' }),
    ).toBeDisabled();
    await openSharingReview(staff.page, second.id);
    const review = staff.page.getByTestId('publisher-withdrawal-review');
    await review
      .getByRole('textbox', { name: 'Private withdrawal review reason', exact: true })
      .fill('PRIVATE OWNER SHARING INVESTIGATION');
    await review
      .getByRole('textbox', { name: 'Reason shared with the resident', exact: true })
      .fill('The reviewed public preview was withdrawn at your request.');
    await review
      .getByLabel('I reviewed the current public preview and this sharing decision.')
      .check();
    // Agency acceptance changes only the private case while publication is paused.
    const caseResponse = await officer.page.request.get(`/api/authority/cases/${caseId}`);
    expect(caseResponse.status()).toBe(200);
    const caseDetail = (await caseResponse.json()) as Schema['CaseDetail'];
    const obligation = caseDetail.obligations[0];
    expect(
      (
        await officer.page.request.post(`/api/authority/obligations/${obligation.id}/accept`, {
          headers: { 'x-jansetu-csrf': '1', 'if-match': `"${obligation.version}"` },
          data: { summary: 'Private work accepted during resident sharing review' },
        })
      ).status(),
    ).toBe(200);
    const stale = staff.page.waitForResponse(
      (r) => r.url().endsWith('/decisions') && r.request().method() === 'POST',
    );
    await review.getByRole('button', { name: 'Record sharing decision' }).click();
    expect((await stale).status()).toBe(412);
    await expect(review).toContainText('Your reason drafts are kept.');
    await review.getByRole('button', { name: 'Refresh withdrawal review' }).click();
    await expect(review.getByTestId('withdrawal-current-preview')).toContainText('Case revision 2');
    await expect(
      review.getByRole('textbox', { name: 'Private withdrawal review reason', exact: true }),
    ).toHaveValue('PRIVATE OWNER SHARING INVESTIGATION');
    await expect(
      review.getByLabel('I reviewed the current public preview and this sharing decision.'),
    ).not.toBeChecked();
    await staff.page.setViewportSize({ width: 320, height: 820 });
    await review
      .getByLabel('I reviewed the current public preview and this sharing decision.')
      .check();
    expect(
      await staff.page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
    ).toBe(true);
    await staff.page.screenshot({ path: 'test-results/publisher-sharing-mobile-light.png' });
    await review.getByRole('button', { name: 'Record sharing decision' }).click();
    await expect(review).toContainText('Recorded outcome');
    await expect(review).toContainText('PRIVATE OWNER SHARING INVESTIGATION');
    await sharing.getByRole('button', { name: 'Refresh sharing review' }).click();
    const approved = sharing.getByTestId(`owned-withdrawal-${second.id}`);
    await expect(approved).toContainText('approved');
    await expect(approved).toContainText(
      'The reviewed public preview was withdrawn at your request.',
    );
    await expect(sharing).not.toContainText('PRIVATE OWNER SHARING INVESTIGATION');
    await expect(sharing.getByRole('link', { name: 'Open public progress' })).toHaveCount(0);
    expect((await page.request.get(`/api/case-receipts/${publication.receiptId}`)).status()).toBe(
      404,
    );
    await page.getByRole('button', { name: 'Close dialog' }).click();
    await page.reload();
    const privateReport = page.locator('.my-report').filter({ hasText: statement });
    await expect(
      privateReport.getByRole('button', { name: 'Manage public sharing' }),
    ).toBeVisible();
    await expect(
      privateReport.getByRole('link', { name: /View reviewed public progress/ }),
    ).toHaveCount(0);
    const after = (await (
      await officer.page.request.get(`/api/authority/cases/${caseId}`)
    ).json()) as Schema['CaseDetail'];
    expect(after.version).toBe(2);
    expect(after.firstReportedAt).toBe(report.receivedAt);
    expect(after.obligations[0].state).toBe('ACCEPTED');
    await openPublicationReview(staff.page, caseId);
    await expect(
      staff.page.getByRole('button', { name: 'Republish reviewed progress' }),
    ).toBeDisabled();
    await expect(
      officer.page.getByRole('button', { name: 'Public sharing', exact: true }),
    ).toHaveCount(0);
  } finally {
    await staff.context.close();
    await officer.context.close();
  }
});

test('resident sharing stale drafts and lost responses recover without duplicate outcomes', async ({
  page,
  browser,
}) => {
  test.setTimeout(120_000);
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  const staff = await staffPage(browser, 'Kiran Shah');
  try {
    const { caseId, report, statement, fields, publication } = await publishedSharingFixture(
      page,
      staff.page,
    );
    await openResidentSharing(page, statement);
    const sharing = page.getByTestId('resident-public-sharing');
    await sharing.getByRole('button', { name: 'Request withdrawal', exact: true }).click();
    await sharing.getByLabel('Reason for withdrawal').selectOption('SHARING_PREFERENCE');
    await sharing
      .getByLabel('I reviewed this public progress and want to request withdrawal.')
      .check();
    expect(
      (
        await staff.page.request.post(`/api/authority/cases/${caseId}/publications`, {
          headers: { 'x-jansetu-csrf': '1', 'if-match': '"1"' },
          data: { ...fields, title: 'Fresh revised sharing preview', publicationVersion: 1 },
        })
      ).status(),
    ).toBe(200);
    const stale = page.waitForResponse(
      (r) =>
        r.url().endsWith('/publication-withdrawal-requests') && r.request().method() === 'POST',
    );
    await sharing.getByRole('button', { name: 'Submit withdrawal request' }).click();
    expect((await stale).status()).toBe(409);
    await expect(sharing.getByLabel('Reason for withdrawal')).toHaveValue('SHARING_PREFERENCE');
    await expect(sharing.getByRole('button', { name: 'Submit withdrawal request' })).toBeDisabled();
    await sharing.getByRole('button', { name: 'Refresh sharing review' }).click();
    await expect(sharing.getByTestId('resident-public-preview')).toContainText('REVISION 2');
    await expect(sharing.getByLabel('Reason for withdrawal')).toHaveValue('SHARING_PREFERENCE');
    await expect(
      sharing.getByLabel('I reviewed this public progress and want to request withdrawal.'),
    ).not.toBeChecked();
    await sharing
      .getByLabel('I reviewed this public progress and want to request withdrawal.')
      .check();
    let committed: Schema['PublicationWithdrawalRequest'] | undefined;
    let command: Schema['PublicationWithdrawalRequestInput'] | undefined;
    let key: string | undefined;
    const createRoute = `**/api/my-reports/${report.id}/publication-withdrawal-requests`;
    await page.route(createRoute, async (route) => {
      command = route.request().postDataJSON() as Schema['PublicationWithdrawalRequestInput'];
      key = route.request().headers()['idempotency-key'];
      const response = await route.fetch();
      expect(response.status()).toBe(201);
      committed = (await response.json()) as Schema['PublicationWithdrawalRequest'];
      await route.abort('failed');
    });
    await sharing.getByRole('button', { name: 'Submit withdrawal request' }).click();
    await expect.poll(() => committed?.state).toBe('REQUESTED');
    await expect(sharing.locator('.form-error')).toContainText('fetch');
    await page.unroute(createRoute);
    if (!committed || !command || !key) throw new Error('Missing committed sharing request');
    const saved = committed;
    const retry = await page.request.post(
      `/api/my-reports/${report.id}/publication-withdrawal-requests`,
      {
        headers: { 'x-jansetu-csrf': '1', 'idempotency-key': key },
        data: command,
      },
    );
    expect(retry.status()).toBe(201);
    expect(((await retry.json()) as Schema['PublicationWithdrawalRequest']).id).toBe(saved.id);
    await sharing.getByRole('button', { name: 'Refresh sharing review' }).click();
    await expect(sharing.getByTestId(`owned-withdrawal-${saved.id}`)).toContainText('requested');
    await expect(sharing.getByRole('button', { name: 'Submit withdrawal request' })).toHaveCount(0);
    const ownerState = (await (
      await page.request.get(`/api/my-reports/${report.id}/public-sharing`)
    ).json()) as Schema['OwnerPublicSharing'];
    expect(ownerState.requests).toHaveLength(1);
    await openSharingReview(staff.page, saved.id);
    const review = staff.page.getByTestId('publisher-withdrawal-review');
    await review.getByRole('combobox', { name: /^Sharing decision/ }).selectOption('DECLINED');
    await review
      .getByRole('textbox', { name: 'Private withdrawal review reason', exact: true })
      .fill('PRIVATE INDEPENDENT SHARING ASSESSMENT');
    await review
      .getByRole('textbox', { name: 'Reason shared with the resident', exact: true })
      .fill('The broad public preview remains available after review.');
    await review
      .getByLabel('I reviewed the current public preview and this sharing decision.')
      .check();
    await staff.page.setViewportSize({ width: 320, height: 820 });
    await staff.page.getByRole('button', { name: 'Use dark theme', exact: true }).click();
    expect(
      await staff.page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
    ).toBe(true);
    await staff.page.screenshot({ path: 'test-results/publisher-sharing-mobile-dark.png' });
    const decisionRoute = `**/api/authority/publication-withdrawal-requests/${saved.id}/decisions`;
    let decisionCommitted = false;
    await staff.page.route(decisionRoute, async (route) => {
      const response = await route.fetch();
      expect(response.status()).toBe(200);
      decisionCommitted = true;
      await route.abort('failed');
    });
    await review.getByRole('button', { name: 'Record sharing decision' }).click();
    await expect.poll(() => decisionCommitted).toBe(true);
    await expect(review.locator('.form-error')).toContainText('fetch');
    await staff.page.unroute(decisionRoute);
    await review.getByRole('button', { name: 'Refresh withdrawal review' }).click();
    await expect(review).toContainText('Recorded outcome');
    await expect(review.getByRole('button', { name: 'Record sharing decision' })).toHaveCount(0);
    await sharing.getByRole('button', { name: 'Refresh sharing review' }).click();
    await expect(sharing.getByTestId(`owned-withdrawal-${saved.id}`)).toContainText('declined');
    await expect(sharing).toContainText('The broad public preview remains available after review.');
    await expect(sharing).not.toContainText('PRIVATE INDEPENDENT SHARING ASSESSMENT');
    expect((await page.request.get(`/api/case-receipts/${publication.receiptId}`)).status()).toBe(
      200,
    );
    expect(
      (
        await staff.page.request.post(`/api/authority/cases/${caseId}/publications`, {
          headers: { 'x-jansetu-csrf': '1', 'if-match': '"1"' },
          data: {
            ...fields,
            title: 'Reviewed correction after declined sharing request',
            publicationVersion: 2,
          },
        })
      ).status(),
    ).toBe(200);
  } finally {
    await staff.context.close();
  }
});

test('structured road reports preserve reviewed details, private exports and coordinator handoff', async ({
  page,
  browser,
}) => {
  test.setTimeout(120_000);
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  await page.getByRole('button', { name: 'Report an issue', exact: true }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Report a service issue' });
  const marker = `Fictional pothole observation ${Date.now()}`;
  const road = `PRIVATE fictional road ${Date.now()}`;
  await dialog.getByLabel('Service category').selectOption('ROAD');
  await expect(dialog).toContainText('Review candidates yourself');
  await dialog.getByLabel('Road name or number').fill(road);
  await dialog.getByLabel('Road type you believe applies').selectOption('NHAI_HIGHWAY');
  const guidance = dialog.getByRole('complementary', { name: 'Road contact guidance' });
  await expect(guidance).toContainText('1033');
  await expect(guidance).toContainText('Contractor unconfirmed');
  await expect(guidance.getByRole('link', { name: 'Official source' })).toHaveAttribute(
    'href',
    'https://ihmcl.co.in/24x7-national-highways-helpline-1033-page/',
  );
  await dialog
    .getByLabel('Direction or lane (optional)')
    .fill('Fictional left lane towards the school');
  await dialog.getByLabel('Location or landmark').fill('PRIVATE fictional school crossing');
  await dialog.getByLabel('Describe the issue').fill(marker);
  await page.setViewportSize({ width: 320, height: 780 });
  await guidance.scrollIntoViewIfNeeded();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: 'test-results/road-guidance-mobile-light.png' });
  await dialog.getByRole('button', { name: 'Review report', exact: true }).click();
  await expect(dialog.getByLabel('Reviewed road details')).toContainText(road);
  await dialog.getByLabel('I have reviewed this fictional report.').check();
  const submitted = page.waitForResponse(
    (r) => r.url().endsWith('/service-reports') && r.request().method() === 'POST',
  );
  await dialog.getByRole('button', { name: 'Submit report', exact: true }).click();
  const response = await submitted;
  expect(response.status()).toBe(201);
  const { id } = (await response.json()) as Schema['ReportAck'];
  await page.getByRole('link', { name: 'View my reports' }).click();
  const card = page.locator('.my-report').filter({ hasText: marker });
  await card.getByText('Saved contact guidance', { exact: true }).click();
  await expect(card).toContainText('1033');
  const downloading = page.waitForEvent('download');
  await card.getByRole('button', { name: 'Download private complaint draft' }).click();
  const download = await downloading;
  expect(download.suggestedFilename()).toBe(`jansetu-road-report-${id}.txt`);
  const path = await download.path();
  expect(path).toBeTruthy();
  const text = readFileSync(path!, 'utf8');
  expect(text).toContain(road);
  expect(text).toContain('PRIVATE fictional school crossing');
  expect(text).toContain('No external complaint has been sent');
  expect(text).toContain('ROAD OWNER / INDIVIDUAL OFFICER / CONTRACTOR: UNCONFIRMED');
  await page.getByRole('button', { name: 'Use dark theme', exact: true }).click();
  await card.scrollIntoViewIfNeeded();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: 'test-results/road-report-mobile-dark.png' });
  const staff = await staffPage(browser, 'Kiran Shah');
  const stranger = await staffPage(browser, 'Rohan Mehta');
  try {
    expect((await stranger.page.request.get(`/api/my-reports/${id}`)).status()).toBe(404);
    expect(await (await stranger.page.request.get('/api/my-reports')).text()).not.toContain(road);
    await staff.page.getByRole('button', { name: 'Service intake', exact: true }).click();
    const intake = staff.page.locator('.staff-card').filter({ hasText: marker });
    await expect(intake.getByLabel('Reviewed road details')).toContainText(road);
    await intake
      .getByLabel('Assessment reason')
      .fill('Fictional road concern reviewed manually for synthetic City Works routing');
    const triaged = staff.page.waitForResponse(
      (r) => r.url().endsWith('/triage') && r.request().method() === 'POST',
    );
    await intake.getByRole('button', { name: 'Create case & propose task' }).click();
    const triage = await triaged;
    expect(triage.status()).toBe(201);
    const { caseId } = (await triage.json()) as { caseId: string };
    const detail = (await (
      await staff.page.request.get(`/api/authority/cases/${caseId}`)
    ).json()) as Schema['CaseDetail'];
    expect(detail.category).toBe('ROAD');
    expect(detail.obligations[0].state).toBe('PROPOSED');
    expect(
      ((await (await page.request.get(`/api/my-reports/${id}`)).json()) as Schema['ReportProgress'])
        .roadDetails?.roadName,
    ).toBe(road);
  } finally {
    await staff.context.close();
    await stranger.context.close();
  }
});

test('private pothole opt-in finds actual candidates and empty scenes without changing the road report', async ({
  page,
  browser,
}) => {
  test.setTimeout(150_000);
  await page.goto('/');
  await signIn(page, 'Rohan Mehta');
  await page.getByRole('button', { name: 'Report an issue', exact: true }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Report a service issue' });
  const statement = `Fictional manually reviewed road ${Date.now()}`;
  await dialog.getByLabel('Service category').selectOption('ROAD');
  await dialog.getByLabel('Road name or number').fill('Fictional candidate review road');
  await dialog.getByLabel('Location or landmark').fill('Fictional candidate crossing');
  await dialog.getByLabel('Describe the issue').fill(statement);
  const analysisRequests: string[][] = [];
  page.on('request', (request) => {
    if (request.url().endsWith('/analyses') && request.method() === 'POST')
      analysisRequests.push((request.postDataJSON() as { tasks: string[] }).tasks);
  });
  await dialog
    .getByLabel('Choose report photos')
    .setInputFiles('tests/fixtures/pothole-positive.png');
  const first = dialog.getByRole('article', { name: 'Photo 1', exact: true });
  const optIn = first.getByLabel('Include experimental pothole recognition');
  await expect(optIn).toBeVisible();
  await expect(optIn).not.toBeChecked();
  expect(analysisRequests).toEqual([]);
  await optIn.check();
  const created = page.waitForResponse(
    (r) => r.url().endsWith('/analyses') && r.request().method() === 'POST',
  );
  await first.getByRole('button', { name: 'Analyze text and potholes', exact: true }).click();
  const analysis = (await (await created).json()) as Schema['Analysis'];
  expect(analysisRequests).toEqual([['QUALITY', 'OCR', 'POTHOLE_DETECTION']]);
  const review = first.getByRole('region', { name: 'Pothole recognition review' });
  await expect(review.getByRole('img', { name: 'Pothole candidate regions' })).toBeVisible({
    timeout: 45_000,
  });
  await expect(review).toContainText('possible pothole');
  await expect(review).toContainText('India and night evaluation is pending');
  await expect(review.locator('polygon')).not.toHaveCount(0);
  await review.getByLabel('Show pothole regions').uncheck();
  await expect(review.locator('polygon')).toHaveCount(0);
  await expect(review).toContainText('Region 1: pothole');
  await review.getByLabel('Show pothole regions').check();
  await expect(dialog.getByLabel('Describe the issue')).toHaveValue(statement);
  await expect(dialog.getByLabel('Service category')).toHaveValue('ROAD');
  await expect(dialog.getByLabel('Road type you believe applies')).toHaveValue('UNKNOWN');
  const result = (await (
    await page.request.get(`/api/analyses/${analysis.id}`)
  ).json()) as Schema['Analysis'];
  const pothole = result.tasks.find((t) => t.kind === 'POTHOLE_DETECTION')!;
  expect(pothole.state).toBe('SUCCEEDED');
  expect(pothole.result?.modelVersion).toContain('/onnx-sha256:');
  expect(pothole.result?.regions.every((r) => r.confidence === null)).toBe(true);
  expect(pothole.result?.codes).toContain('FIELD_EVALUATION_PENDING');
  await page.setViewportSize({ width: 320, height: 780 });
  await review.scrollIntoViewIfNeeded();
  expect(await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
  await page.screenshot({ path: 'test-results/pothole-mobile-light.png' });
  await dialog.getByLabel('Save this private draft on this device').check();
  await dialog.getByRole('button', { name: 'Close dialog', exact: true }).click();
  await page.getByRole('button', { name: 'Use dark theme', exact: true }).click();
  await page.getByRole('button', { name: 'Open navigation', exact: true }).click();
  await page.getByRole('button', { name: 'Report an issue', exact: true }).first().click();
  await dialog.getByRole('button', { name: 'Restore draft', exact: true }).click();
  await expect(review.getByRole('img', { name: 'Pothole candidate regions' })).toBeVisible();
  await review.scrollIntoViewIfNeeded();
  await page.screenshot({ path: 'test-results/pothole-mobile-dark.png' });
  await dialog.getByLabel('Choose report photos').setInputFiles('tests/fixtures/road-texture.png');
  const second = dialog.getByRole('article', { name: 'Photo 2', exact: true });
  await expect(second.getByLabel('Include experimental pothole recognition')).not.toBeChecked();
  await second.getByLabel('Include experimental pothole recognition').check();
  await second.getByRole('button', { name: 'Analyze text and potholes', exact: true }).click();
  await expect(second.getByRole('region', { name: 'Pothole recognition review' })).toContainText(
    'No pothole candidates found',
    { timeout: 45_000 },
  );
  await expect(second).toContainText('This does not mean the scene is safe');
  await expect(dialog.getByLabel('Describe the issue')).toHaveValue(statement);
  const other = await browser.newContext();
  try {
    const foreign = await other.newPage();
    await foreign.goto('/');
    await signIn(foreign, 'Ananya Rao');
    expect((await foreign.request.get(`/api/analyses/${analysis.id}`)).status()).toBe(404);
    expect((await foreign.request.get(`/api/media/${analysis.mediaId}`)).status()).toBe(404);
  } finally {
    await other.close();
  }
  await dialog.getByRole('button', { name: 'Review report', exact: true }).click();
  await dialog.getByLabel('I have reviewed this fictional report.').check();
  const submitted = page.waitForResponse(
    (r) => r.url().endsWith('/service-reports') && r.request().method() === 'POST',
  );
  await dialog.getByRole('button', { name: 'Submit report', exact: true }).click();
  const response = await submitted;
  expect(response.status()).toBe(201);
  const receipt = (await response.json()) as Schema['ReportAck'];
  const own = (await (
    await page.request.get(`/api/my-reports/${receipt.id}`)
  ).json()) as Schema['ReportProgress'];
  expect(own.statement).toBe(statement);
  expect(own.mediaIds).toHaveLength(2);
  expect((response.request().postDataJSON() as Schema['ReportInput']).category).toBe('ROAD');
  expect(own.roadDetails?.roadType).toBe('UNKNOWN');
  const feed = await page.request.get('/api/feed');
  expect(await feed.text()).not.toContain(analysis.id);
});

test('pothole capability outage and category changes preserve opt-out and manual reporting', async ({
  page,
}) => {
  test.setTimeout(100_000);
  await page.route('**/api/capabilities', async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as Schema['Capabilities'];
    body.analysisCapabilities = body.analysisCapabilities.map((c) =>
      c.kind === 'POTHOLE_DETECTION' ? { ...c, status: 'PLANNED' } : c,
    );
    await route.fulfill({ response, json: body });
  });
  await page.goto('/');
  await signIn(page, 'Rohan Mehta');
  await page.getByRole('button', { name: 'Report an issue', exact: true }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Report a service issue' });
  await dialog.getByLabel('Service category').selectOption('ROAD');
  await dialog.getByLabel('Road name or number').fill('Fictional manual fallback road');
  await dialog.getByLabel('Location or landmark').fill('Fictional fallback crossing');
  await dialog
    .getByLabel('Describe the issue')
    .fill('Fictional manual observation without model assistance');
  await dialog.getByLabel('Choose report photos').setInputFiles('tests/fixtures/pothole-big.png');
  await expect(
    dialog.getByRole('button', { name: 'Read text from photo', exact: true }),
  ).toBeVisible();
  await expect(dialog.getByLabel('Include experimental pothole recognition')).toHaveCount(0);
  const created = page.waitForResponse(
    (r) => r.url().endsWith('/analyses') && r.request().method() === 'POST',
  );
  await dialog.getByRole('button', { name: 'Read text from photo', exact: true }).click();
  const response = await created;
  expect((response.request().postDataJSON() as { tasks: string[] }).tasks).toEqual([
    'QUALITY',
    'OCR',
  ]);
  await expect(dialog.getByRole('region', { name: 'Pothole recognition review' })).toHaveCount(0);
  await dialog.getByLabel('Service category').selectOption('FOOTPATH');
  await expect(dialog.getByLabel('Road name or number')).toHaveCount(0);
  await expect(dialog.getByRole('button', { name: 'Review report', exact: true })).toBeEnabled();
});

test('pothole failure review retries only the selected task and preserves completed OCR', async ({
  page,
}) => {
  test.setTimeout(100_000);
  await page.goto('/');
  await signIn(page, 'Rohan Mehta');
  await page.getByRole('button', { name: 'Report an issue', exact: true }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Report a service issue' });
  const marker = 'Fictional unchanged observations while recognition fails';
  await dialog.getByLabel('Service category').selectOption('ROAD');
  await dialog.getByLabel('Road name or number').fill('Fictional retry road');
  await dialog.getByLabel('Location or landmark').fill('Fictional retry crossing');
  await dialog.getByLabel('Describe the issue').fill(marker);
  await dialog.getByLabel('Choose report photos').setInputFiles('tests/fixtures/pothole-big.png');
  await dialog.getByLabel('Include experimental pothole recognition').check();
  let restored = false;
  let finished: Schema['Analysis'] | undefined;
  // UI failure fixture only; real failing-process/retry fencing is exercised by Go integration tests.
  await page.route('**/api/analyses/*', async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as Schema['Analysis'];
    if (body.state === 'SUCCEEDED') {
      finished = body;
      if (!restored) {
        await route.fulfill({
          response,
          json: {
            ...body,
            state: 'PARTIAL',
            tasks: body.tasks.map((t) =>
              t.kind === 'POTHOLE_DETECTION'
                ? {
                    ...t,
                    state: 'FAILED',
                    result: null,
                    retryable: true,
                    errorCode: 'VISION_FAILED',
                  }
                : t,
            ),
          },
        });
        return;
      }
    }
    await route.fulfill({ response });
  });
  await page.route('**/api/analyses/*/retry', async (route) => {
    expect((route.request().postDataJSON() as { tasks: string[] }).tasks).toEqual([
      'POTHOLE_DETECTION',
    ]);
    restored = true;
    await route.fulfill({ status: 202, json: finished });
  });
  await dialog.getByRole('button', { name: 'Analyze text and potholes', exact: true }).click();
  const review = dialog.getByRole('region', { name: 'Pothole recognition review' });
  await expect(review).toContainText('Pothole recognition failed', { timeout: 45_000 });
  await expect(dialog.getByLabel('Describe the issue')).toHaveValue(marker);
  await expect(dialog.getByRole('button', { name: 'Review report', exact: true })).toBeEnabled();
  const preserved = finished!.tasks.filter((t) => t.kind !== 'POTHOLE_DETECTION');
  await review.getByRole('button', { name: 'Retry pothole recognition' }).click();
  await expect(review.getByRole('img', { name: 'Pothole candidate regions' })).toBeVisible({
    timeout: 15_000,
  });
  expect(finished!.tasks.filter((t) => t.kind !== 'POTHOLE_DETECTION')).toEqual(preserved);
  await expect(dialog.getByLabel('Describe the issue')).toHaveValue(marker);
});

test('recorded road footage stays local until an explicitly reviewed frame is attached', async ({
  page,
}) => {
  await page.goto('/');
  await signIn(page, 'Rohan Mehta');
  await page.getByRole('button', { name: 'Use dark theme', exact: true }).click();
  await page.getByRole('button', { name: 'Report an issue', exact: true }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Report a service issue' });
  const marker = `Fictional recorded road observation ${Date.now()}`;
  await dialog.getByLabel('Service category').selectOption('ROAD');
  await dialog.getByLabel('Road name or number').fill('Fictional camera road');
  await dialog.getByLabel('Location or landmark').fill('Fictional road video crossing');
  await dialog.getByLabel('Describe the issue').fill(marker);
  const uploads: string[] = [];
  page.on('request', (r) => {
    if (r.url().endsWith('/media/uploads')) uploads.push(r.url());
  });
  await expect(dialog.getByLabel('Take report photo')).toHaveAttribute('capture', 'environment');
  await dialog
    .getByLabel('Choose recorded road video')
    .setInputFiles('tests/fixtures/road-frame.webm');
  const video = dialog.getByLabel('Local road video preview');
  await expect
    .poll(async () => video.evaluate((v) => (v as HTMLVideoElement).readyState))
    .toBeGreaterThanOrEqual(2);
  expect(uploads).toHaveLength(0);
  await dialog.getByRole('button', { name: 'Review this frame', exact: true }).click();
  await expect(dialog.getByRole('img', { name: /Selected road frame/ })).toBeVisible();
  expect(uploads).toHaveLength(0);
  await page.setViewportSize({ width: 320, height: 780 });
  await dialog.getByRole('button', { name: 'Attach reviewed frame' }).scrollIntoViewIfNeeded();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: 'test-results/road-frame-mobile-dark.png' });
  // A committed allocation may arrive late. Advancing before its response can
  // submit an empty attachment list or leave review waiting on an unmounted card.
  let releaseAllocation = () => {};
  const allocationGate = new Promise<void>((resolve) => {
    releaseAllocation = resolve;
  });
  let allocationCommitted = false;
  await page.route('**/api/media/uploads', async (route) => {
    const response = await route.fetch();
    allocationCommitted = true;
    await allocationGate;
    await route.fulfill({ response });
  });
  try {
    await dialog.getByRole('button', { name: 'Attach reviewed frame' }).click();
    await expect.poll(() => allocationCommitted).toBe(true);
    await expect(dialog.getByRole('button', { name: 'Review report', exact: true })).toBeDisabled();
  } finally {
    releaseAllocation();
  }
  await expect(video).toHaveCount(0);
  await expect(
    dialog.getByRole('img', { name: 'Private report photo 1', exact: true }),
  ).toBeVisible();
  expect(uploads).toHaveLength(1);
  await expect(dialog.getByLabel('Service category')).toHaveValue('ROAD');
  await expect(dialog.getByLabel('Describe the issue')).toHaveValue(marker);
  const analyses = await page.request.get('/api/capabilities');
  expect(
    ((await analyses.json()) as Schema['Capabilities']).analysisCapabilities.find(
      (c) => c.kind === 'POTHOLE_DETECTION',
    )?.status,
  ).toBe('EVALUATING');
  await dialog.getByRole('button', { name: 'Review report', exact: true }).click();
  await dialog.getByLabel('I have reviewed this fictional report.').check();
  const submitted = page.waitForResponse(
    (r) => r.url().endsWith('/service-reports') && r.request().method() === 'POST',
  );
  await dialog.getByRole('button', { name: 'Submit report', exact: true }).click();
  const response = await submitted;
  expect(response.status()).toBe(201);
  const { id } = (await response.json()) as Schema['ReportAck'];
  const own = (await (
    await page.request.get(`/api/my-reports/${id}`)
  ).json()) as Schema['ReportProgress'];
  expect(own.mediaIds).toHaveLength(1);
  expect(own.roadGuidance?.status).toBe('UNAVAILABLE');
  expect(own.roadGuidance?.contacts).toEqual([]);
});

test('road drafts recover observations and manual reporting survives unavailable guidance and invalid video', async ({
  page,
}) => {
  await page.goto('/');
  await signIn(page, 'Ananya Rao');
  await page.route('**/api/road-guidance?*', (route) => route.abort());
  await page.getByRole('button', { name: 'Report an issue', exact: true }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Report a service issue' });
  await dialog.getByLabel('Service category').selectOption('ROAD');
  await dialog.getByLabel('Road name or number').fill('Fictional recovered road');
  await dialog.getByLabel('Observed surface issue').selectOption('BROKEN_SURFACE');
  await dialog.getByLabel('Road type you believe applies').selectOption('KARNATAKA_PWD');
  await dialog.getByLabel('Direction or lane (optional)').fill('Fictional westbound lane');
  await dialog.getByLabel('Location or landmark').fill('Fictional restored road crossing');
  const marker = `Fictional manual road fallback ${Date.now()}`;
  await dialog.getByLabel('Describe the issue').fill(marker);
  await dialog.getByLabel('Choose recorded road video').setInputFiles({
    name: 'invalid-road.webm',
    mimeType: 'video/webm',
    buffer: Buffer.from('not a video'),
  });
  await expect(dialog).toContainText('This video format could not be read');
  await expect(dialog.getByRole('button', { name: 'Review report', exact: true })).toBeEnabled();
  await dialog.getByLabel('Save this private draft on this device').check();
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          Object.entries(localStorage).find(([k]) => k.startsWith('jansetu.report-draft.'))?.[1] ||
          '',
      ),
    )
    .toContain('Fictional recovered road');
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await page.locator('main').getByRole('button', { name: 'Report an issue', exact: true }).click();
  await dialog.getByRole('button', { name: 'Restore draft', exact: true }).click();
  await expect(dialog.getByLabel('Road name or number')).toHaveValue('Fictional recovered road');
  await expect(dialog.getByLabel('Observed surface issue')).toHaveValue('BROKEN_SURFACE');
  await expect(dialog.getByLabel('Direction or lane (optional)')).toHaveValue(
    'Fictional westbound lane',
  );
  // A category change must not silently include stale road observations.
  await dialog.getByLabel('Service category').selectOption('LIGHT');
  await expect(dialog.getByLabel('Road name or number')).toHaveCount(0);
  await dialog.getByRole('button', { name: 'Review report', exact: true }).click();
  await dialog.getByLabel('I have reviewed this fictional report.').check();
  const submitted = page.waitForResponse(
    (r) => r.url().endsWith('/service-reports') && r.request().method() === 'POST',
  );
  await dialog.getByRole('button', { name: 'Submit report', exact: true }).click();
  const response = await submitted;
  expect(response.request().postDataJSON()).not.toHaveProperty('roadDetails');
  expect(response.status()).toBe(201);
  const { id } = (await response.json()) as Schema['ReportAck'];
  expect(
    ((await (await page.request.get(`/api/my-reports/${id}`)).json()) as Schema['ReportProgress'])
      .roadDetails,
  ).toBeNull();
});

test('required task proposals recover lost acknowledgements and all work needs independent verification', async ({
  page,
  browser,
}) => {
  test.setTimeout(120_000);
  await page.goto('/');
  await signIn(page, 'Rohan Mehta');
  const coordinator = await staffPage(browser, 'Kiran Shah');
  const officer = await staffPage(browser, 'City Works team');
  const verifier = await staffPage(browser, 'Neha Sen');
  const csrf = { 'x-jansetu-csrf': '1' };
  try {
    const { caseId, report, statement } = await publicProgressFixture(page, coordinator.page);
    const path = `/api/authority/cases/${caseId}`;
    let detail = (await (await coordinator.page.request.get(path)).json()) as Schema['CaseDetail'];
    const first = detail.obligations[0].id;
    const published = await coordinator.page.request.post(`${path}/publications`, {
      headers: { ...csrf, 'if-match': '"1"' },
      data: {
        title: 'Fictional joint restoration',
        summary: 'Reviewed fictional work is awaiting acceptance.',
        area: 'Fictional broad area',
        reviewed: true,
        publicationVersion: 0,
        reason: 'Private synthetic publication review',
      },
    });
    expect(published.status()).toBe(200);
    const { receiptId } = (await published.json()) as Schema['PublicationResult'];
    await openPublicationReview(coordinator.page, caseId);
    const proposal = coordinator.page.getByTestId('task-proposal');
    const scope = 'PRIVATE restore the separate pedestrian crossing surface';
    await proposal
      .getByLabel('Agency for this task')
      .selectOption('30000000-0000-4000-8000-000000000001');
    await proposal.getByLabel('Required work scope').fill(scope);
    const prerequisite = proposal.getByRole('checkbox', { name: /Task 1 ·/ });
    await prerequisite.check();
    let loseAck = true;
    let taskId = '';
    let proposalId = '';
    await coordinator.page.route(`**${path}/obligations`, async (route) => {
      if (!loseAck) return route.continue();
      loseAck = false;
      proposalId = (route.request().postDataJSON() as Schema['TaskProposalInput']).clientTaskId;
      const response = await route.fetch();
      expect(response.status()).toBe(201);
      taskId = ((await response.json()) as Schema['TaskProposalResult']).id;
      await route.abort('failed');
    });
    await proposal.getByRole('button', { name: 'Propose required task', exact: true }).click();
    await expect(proposal.getByRole('alert')).toBeVisible();
    await expect(proposal.getByLabel('Required work scope')).toHaveValue(scope);
    await expect(prerequisite).toBeChecked();
    const retry = coordinator.page.waitForResponse(
      (r) => r.url().endsWith(`${path}/obligations`) && r.request().method() === 'POST',
    );
    await proposal.getByRole('button', { name: 'Propose required task', exact: true }).click();
    const retryResponse = await retry;
    expect(retryResponse.status()).toBe(201);
    expect(
      (retryResponse.request().postDataJSON() as Schema['TaskProposalInput']).clientTaskId,
    ).toBe(proposalId);
    expect(((await retryResponse.json()) as Schema['TaskProposalResult']).id).toBe(taskId);
    await expect(coordinator.page.getByTestId(`obligation-${taskId}`)).toContainText(scope);
    detail = (await (await coordinator.page.request.get(path)).json()) as Schema['CaseDetail'];
    expect(detail.obligations).toHaveLength(2);
    expect(detail.version).toBe(2);
    expect(detail.firstReportedAt).toBe(report.receivedAt);
    await officer.page.reload();
    await officer.page.getByTestId(`staff-case-${caseId}`).click();
    const firstCard = officer.page.getByTestId(`obligation-${first}`);
    const secondCard = officer.page.getByTestId(`obligation-${taskId}`);
    await firstCard
      .getByLabel('Work or decision summary')
      .fill('Fictional first task accepted independently');
    await expect(secondCard.getByLabel('Work or decision summary')).toHaveValue('');
    for (const label of ['Accept task', 'Start work', 'Claim completion']) {
      await firstCard
        .getByLabel('Work or decision summary')
        .fill('Fictional first task stage documented independently');
      await firstCard.getByRole('button', { name: label, exact: true }).click();
      await expect(firstCard.getByRole('button', { name: label, exact: true })).not.toBeVisible();
    }
    await secondCard
      .getByLabel('Work or decision summary')
      .fill('Fictional second required task accepted separately');
    await secondCard.getByRole('button', { name: 'Accept task', exact: true }).click();
    await expect(secondCard.getByRole('button', { name: 'Start work', exact: true })).toBeVisible();
    await expect(
      secondCard.getByRole('button', { name: 'Start work', exact: true }),
    ).toBeDisabled();
    await expect(secondCard.getByTestId('task-sequence')).toContainText(
      'Work blocked: prerequisite verification pending.',
    );
    const blockedStart = await officer.page.request.post(
      `/api/authority/obligations/${taskId}/start`,
      {
        headers: { ...csrf, 'if-match': '"2"' },
        data: { summary: 'Attempted work before independent prerequisite verification' },
      },
    );
    expect(blockedStart.status()).toBe(409);
    expect(((await blockedStart.json()) as Schema['Problem']).code).toBe(
      'TASK_PREREQUISITES_PENDING',
    );
    await expect(officer.page.locator('.case-detail .receipt-eyebrow .badge')).toHaveText(
      'verification pending',
    );
    await verifier.page.reload();
    await verifier.page.getByTestId(`staff-case-${caseId}`).click();
    const firstReview = verifier.page.getByTestId(`obligation-${first}`);
    await firstReview
      .getByLabel('Independent inspection reason')
      .fill('Independent fictional inspection of the first restored surface');
    await firstReview.getByRole('button', { name: 'Record verification decision' }).click();
    await expect(verifier.page.getByTestId('restoration-summary')).toContainText(
      '1 of 2 required tasks verified',
    );
    await expect(verifier.page.locator('.case-detail .receipt-eyebrow .badge')).toHaveText(
      'active',
    );
    expect(
      (
        (await (
          await page.request.get(`/api/my-reports/${report.id}`)
        ).json()) as Schema['ReportProgress']
      ).state,
    ).toBe('ACCEPTED');
    await officer.page.reload();
    await officer.page.getByTestId(`staff-case-${caseId}`).click();
    await expect(secondCard.getByRole('button', { name: 'Start work', exact: true })).toBeEnabled();
    await expect(secondCard.getByTestId('task-sequence')).toContainText(
      'Prerequisites independently verified. Work can proceed.',
    );
    for (const label of ['Start work', 'Claim completion']) {
      await secondCard
        .getByLabel('Work or decision summary')
        .fill('Fictional second task work performed and documented');
      await secondCard.getByRole('button', { name: label, exact: true }).click();
      await expect(secondCard.getByRole('button', { name: label, exact: true })).not.toBeVisible();
    }
    await verifier.page.reload();
    await verifier.page.getByTestId(`staff-case-${caseId}`).click();
    const secondReview = verifier.page.getByTestId(`obligation-${taskId}`);
    await secondReview
      .getByLabel('Independent inspection reason')
      .fill('Independent fictional inspection of the separate restored crossing');
    await secondReview.getByRole('button', { name: 'Record verification decision' }).click();
    await expect(verifier.page.locator('.case-detail .receipt-eyebrow .badge')).toHaveText(
      'resolved',
    );
    await expect(verifier.page.getByTestId('restoration-summary')).toHaveText(
      '2 of 2 required tasks verified.',
    );
    detail = (await (await coordinator.page.request.get(path)).json()) as Schema['CaseDetail'];
    expect(detail.canProposeTask).toBe(false);
    expect(detail.firstReportedAt).toBe(report.receivedAt);
    const publicBefore = (await (
      await page.request.get(`/api/case-receipts/${receiptId}`)
    ).json()) as Schema['Receipt'];
    expect(publicBefore.state).toBe('OPEN');
    expect(publicBefore.responsibilities).toHaveLength(1);
    expect(JSON.stringify(publicBefore)).not.toContain(scope);
    const corrected = await coordinator.page.request.post(`${path}/publications`, {
      headers: { ...csrf, 'if-match': `"${detail.version}"` },
      data: {
        title: 'Fictional joint restoration verified',
        summary: 'Both required tasks received independent fictional verification.',
        area: 'Fictional broad area',
        reviewed: true,
        publicationVersion: 1,
        reason: 'Private synthetic final publication review',
      },
    });
    expect(corrected.status()).toBe(200);
    const publicAfter = (await (
      await page.request.get(`/api/case-receipts/${receiptId}`)
    ).json()) as Schema['Receipt'];
    expect(publicAfter.state).toBe('RESOLVED');
    expect(publicAfter.responsibilities).toHaveLength(2);
    for (const secret of [scope, statement, caseId, report.id, first, taskId, proposalId])
      expect(JSON.stringify(publicAfter)).not.toContain(secret);
    await openPublicationReview(coordinator.page, caseId);
    await expect(coordinator.page.getByTestId('task-proposal')).not.toBeVisible();
    await expect(
      coordinator.page.getByText('This case is closed to new task proposals.'),
    ).toBeVisible();
  } finally {
    await coordinator.context.close();
    await officer.context.close();
    await verifier.context.close();
  }
});

test('multi-agency proposals preserve stale drafts, agency scope and mobile layout', async ({
  page,
  browser,
}) => {
  test.setTimeout(90_000);
  await page.goto('/');
  await signIn(page, 'Rohan Mehta');
  const coordinator = await staffPage(browser, 'Kiran Shah');
  const officer = await staffPage(browser, 'City Works team');
  const csrf = { 'x-jansetu-csrf': '1' };
  try {
    const { caseId, report } = await publicProgressFixture(page, coordinator.page);
    const path = `/api/authority/cases/${caseId}`;
    await openPublicationReview(coordinator.page, caseId);
    const proposal = coordinator.page.getByTestId('task-proposal');
    const scope = 'PRIVATE inspect and restore the fictional water service — पानी';
    await proposal
      .getByLabel('Agency for this task')
      .selectOption('30000000-0000-4000-8000-000000000002');
    await proposal.getByLabel('Required work scope').fill(scope);
    const prerequisite = proposal.getByRole('checkbox', { name: /Task 1 ·/ });
    await prerequisite.check();
    const competing = await coordinator.page.request.post(`${path}/obligations`, {
      headers: { ...csrf, 'if-match': '"1"' },
      data: {
        clientTaskId: crypto.randomUUID(),
        agencyId: '30000000-0000-4000-8000-000000000001',
        scope: 'PRIVATE remaining separate road restoration assessment',
      },
    });
    expect(competing.status()).toBe(201);
    const stale = coordinator.page.waitForResponse(
      (r) => r.url().endsWith(`${path}/obligations`) && r.request().method() === 'POST',
    );
    await proposal.getByRole('button', { name: 'Propose required task', exact: true }).click();
    expect((await stale).status()).toBe(412);
    await expect(proposal.getByRole('alert')).toContainText('Refresh');
    await proposal.getByRole('button', { name: 'Refresh case', exact: true }).click();
    await expect(coordinator.page.getByTestId('restoration-summary')).toContainText(
      '0 of 2 required tasks verified',
    );
    await expect(proposal.getByLabel('Required work scope')).toHaveValue(scope);
    await expect(prerequisite).toBeChecked();
    const saved = coordinator.page.waitForResponse(
      (r) => r.url().endsWith(`${path}/obligations`) && r.request().method() === 'POST',
    );
    await proposal.getByRole('button', { name: 'Propose required task', exact: true }).click();
    const response = await saved;
    expect(response.status()).toBe(201);
    const waterId = ((await response.json()) as Schema['TaskProposalResult']).id;
    const waterCard = coordinator.page.getByTestId(`obligation-${waterId}`);
    await expect(waterCard).toContainText(scope);
    await coordinator.page.setViewportSize({ width: 320, height: 780 });
    await waterCard.scrollIntoViewIfNeeded();
    await expect
      .poll(() =>
        coordinator.page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
      )
      .toBe(true);
    await coordinator.page.screenshot({ path: 'test-results/multi-agency-mobile-light.png' });
    await coordinator.page.getByRole('button', { name: 'Use dark theme', exact: true }).click();
    await waterCard.scrollIntoViewIfNeeded();
    await expect
      .poll(() =>
        coordinator.page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
      )
      .toBe(true);
    await coordinator.page.screenshot({ path: 'test-results/multi-agency-mobile-dark.png' });
    await officer.page.reload();
    await officer.page.getByTestId(`staff-case-${caseId}`).click();
    const otherAgency = officer.page.getByTestId(`obligation-${waterId}`);
    await expect(otherAgency).toContainText('Water Services (demo)');
    await expect(otherAgency).toContainText('Required for restoration');
    await expect(otherAgency).toContainText('Deadline unavailable');
    await expect(otherAgency.getByTestId('task-sequence')).toContainText(
      'Work blocked: prerequisite verification pending.',
    );
    await expect(
      otherAgency.getByRole('button', { name: 'Accept task', exact: true }),
    ).not.toBeVisible();
    await expect(officer.page.getByTestId('task-proposal')).not.toBeVisible();
    const own = (await (
      await page.request.get(`/api/my-reports/${report.id}`)
    ).json()) as Schema['ReportProgress'];
    expect(own.state).toBe('AWAITING_AGENCY_ACCEPTANCE');
    expect(own.responsibilities).toHaveLength(3);
    expect(JSON.stringify(own)).not.toContain(scope);
  } finally {
    await coordinator.context.close();
    await officer.context.close();
  }
});

test('dependent work waits for every prerequisite and insufficient evidence keeps it blocked', async ({
  page,
  browser,
}) => {
  test.setTimeout(120_000);
  await page.goto('/');
  await signIn(page, 'Rohan Mehta');
  const coordinator = await staffPage(browser, 'Kiran Shah');
  const officer = await staffPage(browser, 'City Works team');
  const verifier = await staffPage(browser, 'Neha Sen');
  const csrf = { 'x-jansetu-csrf': '1' };
  try {
    const { caseId, report } = await publicProgressFixture(page, coordinator.page);
    const path = `/api/authority/cases/${caseId}`;
    let detail = (await (await coordinator.page.request.get(path)).json()) as Schema['CaseDetail'];
    const first = detail.obligations[0].id;
    const independent = await coordinator.page.request.post(`${path}/obligations`, {
      headers: { ...csrf, 'if-match': '"1"' },
      data: {
        clientTaskId: crypto.randomUUID(),
        agencyId: '30000000-0000-4000-8000-000000000001',
        scope: 'PRIVATE additional prerequisite surface inspection',
      },
    });
    expect(independent.status()).toBe(201);
    const second = ((await independent.json()) as Schema['TaskProposalResult']).id;
    await openPublicationReview(coordinator.page, caseId);
    const proposal = coordinator.page.getByTestId('task-proposal');
    await proposal
      .getByLabel('Agency for this task')
      .selectOption('30000000-0000-4000-8000-000000000001');
    await proposal
      .getByLabel('Required work scope')
      .fill('PRIVATE finish restoration after both independent inspections');
    await proposal.getByRole('checkbox', { name: /Task 1 ·/ }).check();
    await proposal.getByRole('checkbox', { name: /Task 2 ·/ }).check();
    const saving = coordinator.page.waitForResponse(
      (r) => r.url().endsWith(`${path}/obligations`) && r.request().method() === 'POST',
    );
    await proposal.getByRole('button', { name: 'Propose required task', exact: true }).click();
    const saved = await saving;
    expect(saved.status()).toBe(201);
    const third = ((await saved.json()) as Schema['TaskProposalResult']).id;
    await expect(proposal.getByRole('checkbox', { name: /Task 1 ·/ })).not.toBeChecked();
    await officer.page.reload();
    await officer.page.getByTestId(`staff-case-${caseId}`).click();
    const dependent = officer.page.getByTestId(`obligation-${third}`);
    await dependent
      .getByLabel('Work or decision summary')
      .fill('Fictional acceptance while prerequisite work is pending');
    await dependent.getByRole('button', { name: 'Accept task', exact: true }).click();
    await expect(dependent.getByRole('button', { name: 'Start work', exact: true })).toBeDisabled();
    const perform = async (id: string, labels: string[]) => {
      const card = officer.page.getByTestId(`obligation-${id}`);
      for (const label of labels) {
        await card
          .getByLabel('Work or decision summary')
          .fill('Fictional prerequisite stage performed and documented');
        await card.getByRole('button', { name: label, exact: true }).click();
        await expect(card.getByRole('button', { name: label, exact: true })).not.toBeVisible();
      }
    };
    const inspect = async (id: string, result: string) => {
      await verifier.page.reload();
      await verifier.page.getByTestId(`staff-case-${caseId}`).click();
      const card = verifier.page.getByTestId(`obligation-${id}`);
      await card.getByLabel('Inspection result').selectOption(result);
      await card
        .getByLabel('Independent inspection reason')
        .fill('Independent fictional prerequisite inspection with documented result');
      const response = verifier.page.waitForResponse(
        (r) =>
          r.url().endsWith(`${path}/verification-decisions`) && r.request().method() === 'POST',
      );
      await card.getByRole('button', { name: 'Record verification decision' }).click();
      expect((await response).status()).toBe(200);
      await officer.page.reload();
      await officer.page.getByTestId(`staff-case-${caseId}`).click();
    };
    await perform(first, ['Accept task', 'Start work', 'Claim completion']);
    await inspect(first, 'INSUFFICIENT');
    await expect(dependent.getByRole('button', { name: 'Start work', exact: true })).toBeDisabled();
    await expect(
      dependent
        .getByTestId('task-sequence')
        .getByText('Awaiting independent verification', { exact: true }),
    ).toHaveCount(2);
    await inspect(first, 'VERIFIED');
    await expect(dependent.getByRole('button', { name: 'Start work', exact: true })).toBeDisabled();
    await expect(
      dependent
        .getByTestId('task-sequence')
        .getByText('Awaiting independent verification', { exact: true }),
    ).toHaveCount(1);
    await perform(second, ['Accept task', 'Start work', 'Claim completion']);
    await expect(dependent.getByRole('button', { name: 'Start work', exact: true })).toBeDisabled();
    await inspect(second, 'VERIFIED');
    await expect(dependent.getByRole('button', { name: 'Start work', exact: true })).toBeEnabled();
    await perform(third, ['Start work']);
    detail = (await (await coordinator.page.request.get(path)).json()) as Schema['CaseDetail'];
    const finalTask = detail.obligations.find((o) => o.id === third);
    expect(finalTask?.prerequisiteTaskIds.slice().sort()).toEqual([first, second].sort());
    expect(finalTask?.blockedByTaskIds).toEqual([]);
    expect(finalTask?.state).toBe('IN_PROGRESS');
    expect(detail.state).toBe('ACTIVE');
    expect(detail.firstReportedAt).toBe(report.receivedAt);
  } finally {
    await coordinator.context.close();
    await officer.context.close();
    await verifier.context.close();
  }
});

test('reviewed partial acceptance recovers lost acknowledgements and preserves required work', async ({
  page,
  browser,
}) => {
  test.setTimeout(120_000);
  await page.goto('/');
  await signIn(page, 'Rohan Mehta');
  const coordinator = await staffPage(browser, 'Kiran Shah');
  const officer = await staffPage(browser, 'City Works team');
  const verifier = await staffPage(browser, 'Neha Sen');
  const csrf = { 'x-jansetu-csrf': '1' };
  try {
    const { caseId, report, statement } = await publicProgressFixture(page, coordinator.page);
    const path = `/api/authority/cases/${caseId}`;
    let detail = (await (await coordinator.page.request.get(path)).json()) as Schema['CaseDetail'];
    const original = detail.obligations[0].id;
    const initialPublication = await coordinator.page.request.post(`${path}/publications`, {
      headers: { ...csrf, 'if-match': '"1"' },
      data: {
        title: 'Fictional partial restoration assessment',
        summary: 'Local work remains subject to independent verification.',
        area: 'Fictional broad area',
        reviewed: true,
        publicationVersion: 0,
        reason: 'Private initial progress review',
      },
    });
    expect(initialPublication.status()).toBe(200);
    const receiptId = ((await initialPublication.json()) as Schema['PublicationResult']).receiptId;
    await officer.page.reload();
    await officer.page.getByTestId(`staff-case-${caseId}`).click();
    const partial = officer.page.getByTestId(`partial-acceptance-${original}`);
    await partial.getByRole('button', { name: 'Accept part of this task', exact: true }).click();
    const acceptedScope = 'PRIVATE restore the north pavement portion — फुटपाथ';
    const remainingScope = 'PRIVATE restore the separate south pavement portion';
    const reason = 'PRIVATE agency can commit to the north portion initially';
    await partial.getByLabel('Scope your agency can accept').fill(acceptedScope);
    await partial.getByLabel('Remaining required scope').fill(remainingScope);
    await partial.getByLabel('Partial acceptance reason').fill(reason);
    let loseProposal = true;
    let clientRequestId = '';
    let requestId = '';
    const proposalPath = `/api/authority/obligations/${original}/partial-acceptances`;
    await officer.page.route(`**${proposalPath}`, async (route) => {
      if (!loseProposal) return route.continue();
      loseProposal = false;
      clientRequestId = (route.request().postDataJSON() as Schema['PartialAcceptanceInput'])
        .clientRequestId;
      const saved = await route.fetch();
      expect(saved.status()).toBe(201);
      requestId = ((await saved.json()) as Schema['TaskSplitResult']).id;
      await route.abort('failed');
    });
    await partial.getByRole('button', { name: 'Request partial acceptance', exact: true }).click();
    await expect(partial.getByRole('alert')).toBeVisible();
    await expect(partial.getByLabel('Scope your agency can accept')).toHaveValue(acceptedScope);
    await expect(partial.getByLabel('Remaining required scope')).toHaveValue(remainingScope);
    const retried = officer.page.waitForResponse(
      (r) => r.url().endsWith(proposalPath) && r.request().method() === 'POST',
    );
    await partial.getByRole('button', { name: 'Request partial acceptance', exact: true }).click();
    const retry = await retried;
    expect(retry.status()).toBe(201);
    expect(
      (retry.request().postDataJSON() as Schema['PartialAcceptanceInput']).clientRequestId,
    ).toBe(clientRequestId);
    expect(((await retry.json()) as Schema['TaskSplitResult']).id).toBe(requestId);
    const pending = officer.page.getByTestId(`task-split-${requestId}`);
    await expect(pending).toContainText('Whole-task acceptance is paused');
    await expect(
      pending.getByRole('button', { name: 'Confirm scope split', exact: true }),
    ).not.toBeVisible();
    detail = (await (await coordinator.page.request.get(path)).json()) as Schema['CaseDetail'];
    expect(detail.obligations).toHaveLength(1);
    expect(detail.taskSplitRequests).toHaveLength(1);
    const fullAcceptance = await officer.page.request.post(
      `/api/authority/obligations/${original}/accept`,
      {
        headers: { ...csrf, 'if-match': `"${detail.obligations[0].version}"` },
        data: { summary: 'Attempt whole acceptance while scope review is pending' },
      },
    );
    expect(fullAcceptance.status()).toBe(409);
    await openPublicationReview(coordinator.page, caseId);
    const review = coordinator.page.getByTestId(`task-split-${requestId}`);
    await review
      .getByLabel('Agency for remaining required work')
      .selectOption('30000000-0000-4000-8000-000000000001');
    await review
      .getByRole('checkbox', { name: 'Both scopes fully cover the original work without overlap.' })
      .check();
    await review
      .getByLabel('Scope split review reason')
      .fill('PRIVATE independent review confirms complete scope coverage');
    await coordinator.page.setViewportSize({ width: 320, height: 780 });
    await review.scrollIntoViewIfNeeded();
    await expect
      .poll(() =>
        coordinator.page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
      )
      .toBe(true);
    await coordinator.page.screenshot({ path: 'test-results/partial-acceptance-mobile-light.png' });
    await coordinator.page.getByRole('button', { name: 'Use dark theme', exact: true }).click();
    await review.scrollIntoViewIfNeeded();
    await expect
      .poll(() =>
        coordinator.page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
      )
      .toBe(true);
    await coordinator.page.screenshot({ path: 'test-results/partial-acceptance-mobile-dark.png' });
    let loseDecision = true;
    let approved: Schema['TaskSplitResult'] | undefined;
    await coordinator.page.route(`**${path}/task-split-decisions`, async (route) => {
      if (!loseDecision) return route.continue();
      loseDecision = false;
      const saved = await route.fetch();
      expect(saved.status()).toBe(200);
      approved = (await saved.json()) as Schema['TaskSplitResult'];
      await route.abort('failed');
    });
    await review.getByRole('button', { name: 'Confirm scope split', exact: true }).click();
    await expect(review.getByRole('alert')).toBeVisible();
    await expect(review.getByRole('checkbox')).toBeChecked();
    const confirmed = coordinator.page.waitForResponse(
      (r) => r.url().endsWith(`${path}/task-split-decisions`) && r.request().method() === 'POST',
    );
    await review.getByRole('button', { name: 'Confirm scope split', exact: true }).click();
    const confirmedResponse = await confirmed;
    expect(confirmedResponse.status()).toBe(200);
    expect(((await confirmedResponse.json()) as Schema['TaskSplitResult']).acceptedTaskId).toBe(
      approved?.acceptedTaskId,
    );
    await expect(review).toContainText('Original scope replaced by two required tasks.');
    detail = (await (await coordinator.page.request.get(path)).json()) as Schema['CaseDetail'];
    expect(detail.obligations).toHaveLength(3);
    expect(detail.obligations.find((o) => o.id === original)?.scopeReplaced).toBe(true);
    const accepted = approved!.acceptedTaskId!;
    const remaining = approved!.remainingTaskId!;
    await officer.page.reload();
    await officer.page.getByTestId(`staff-case-${caseId}`).click();
    const perform = async (id: string, labels: string[]) => {
      const card = officer.page.getByTestId(`obligation-${id}`);
      for (const label of labels) {
        await card
          .getByLabel('Work or decision summary')
          .fill('Fictional defined scope work recorded independently');
        await card.getByRole('button', { name: label, exact: true }).click();
        await expect(card.getByRole('button', { name: label, exact: true })).not.toBeVisible();
      }
    };
    const inspect = async (id: string) => {
      await verifier.page.reload();
      await verifier.page.getByTestId(`staff-case-${caseId}`).click();
      const card = verifier.page.getByTestId(`obligation-${id}`);
      await card
        .getByLabel('Independent inspection reason')
        .fill('Independent fictional inspection confirms this complete scope');
      const response = verifier.page.waitForResponse(
        (r) =>
          r.url().endsWith(`${path}/verification-decisions`) && r.request().method() === 'POST',
      );
      await card.getByRole('button', { name: 'Record verification decision' }).click();
      expect((await response).status()).toBe(200);
      await officer.page.reload();
      await officer.page.getByTestId(`staff-case-${caseId}`).click();
    };
    await perform(accepted, ['Start work', 'Claim completion']);
    await inspect(accepted);
    await expect(officer.page.getByTestId('restoration-summary')).toContainText(
      '1 of 2 required tasks verified',
    );
    let ownerProgress = (await (
      await page.request.get(`/api/my-reports/${report.id}`)
    ).json()) as Schema['ReportProgress'];
    expect(ownerProgress.state).not.toBe('VERIFIED');
    expect(ownerProgress.responsibilities).toHaveLength(2);
    await perform(remaining, ['Accept task', 'Start work', 'Claim completion']);
    await inspect(remaining);
    detail = (await (await coordinator.page.request.get(path)).json()) as Schema['CaseDetail'];
    expect(detail.state).toBe('RESOLVED');
    expect(detail.firstReportedAt).toBe(report.receivedAt);
    ownerProgress = (await (
      await page.request.get(`/api/my-reports/${report.id}`)
    ).json()) as Schema['ReportProgress'];
    expect(ownerProgress.state).toBe('VERIFIED');
    const before = (await (
      await page.request.get(`/api/case-receipts/${receiptId}`)
    ).json()) as Schema['Receipt'];
    expect(before.state).toBe('OPEN');
    expect(before.responsibilities).toHaveLength(1);
    const publication = await coordinator.page.request.post(`${path}/publications`, {
      headers: { ...csrf, 'if-match': `"${detail.version}"` },
      data: {
        title: 'Fictional split scopes independently verified',
        summary: 'All required work received independent fictional review.',
        area: 'Fictional broad area',
        reviewed: true,
        publicationVersion: 1,
        reason: 'Private final progress review',
      },
    });
    expect(publication.status()).toBe(200);
    const after = (await (
      await page.request.get(`/api/case-receipts/${receiptId}`)
    ).json()) as Schema['Receipt'];
    expect(after.state).toBe('RESOLVED');
    expect(after.responsibilities).toHaveLength(2);
    for (const secret of [
      acceptedScope,
      remainingScope,
      reason,
      statement,
      caseId,
      report.id,
      requestId,
      original,
      accepted,
      remaining,
      clientRequestId,
    ]) {
      expect(JSON.stringify(after)).not.toContain(secret);
      if (secret !== statement && secret !== report.id)
        expect(JSON.stringify(ownerProgress)).not.toContain(secret);
    }
  } finally {
    await coordinator.context.close();
    await officer.context.close();
    await verifier.context.close();
  }
});

test('partial acceptance review keeps stale drafts and rejection preserves original work', async ({
  page,
  browser,
}) => {
  test.setTimeout(90_000);
  await page.goto('/');
  await signIn(page, 'Rohan Mehta');
  const coordinator = await staffPage(browser, 'Kiran Shah');
  const officer = await staffPage(browser, 'City Works team');
  const csrf = { 'x-jansetu-csrf': '1' };
  try {
    const { caseId } = await publicProgressFixture(page, coordinator.page);
    const path = `/api/authority/cases/${caseId}`;
    let detail = (await (await coordinator.page.request.get(path)).json()) as Schema['CaseDetail'];
    const original = detail.obligations[0].id;
    const proposal = await officer.page.request.post(
      `/api/authority/obligations/${original}/partial-acceptances`,
      {
        headers: { ...csrf, 'if-match': '"1"' },
        data: {
          clientRequestId: crypto.randomUUID(),
          acceptedScope: 'PRIVATE original accepted scope proposal',
          remainingScope: 'PRIVATE original remaining scope proposal',
          reason: 'PRIVATE agency commitment needs coordinator review',
          authorityBasisRef: 'synthetic-local-mandate-v1',
        },
      },
    );
    expect(proposal.status()).toBe(201);
    const requestId = ((await proposal.json()) as Schema['TaskSplitResult']).id;
    await openPublicationReview(coordinator.page, caseId);
    const review = coordinator.page.getByTestId(`task-split-${requestId}`);
    const reason = 'PRIVATE coordinator requests clearer complete scope accounting';
    await review.getByLabel('Scope split decision').selectOption('REJECT');
    await review.getByLabel('Scope split review reason').fill(reason);
    const competing = await coordinator.page.request.post(`${path}/obligations`, {
      headers: { ...csrf, 'if-match': '"2"' },
      data: {
        clientTaskId: crypto.randomUUID(),
        agencyId: '30000000-0000-4000-8000-000000000001',
        scope: 'PRIVATE separate inspection work added during scope review',
      },
    });
    expect(competing.status()).toBe(201);
    const stale = coordinator.page.waitForResponse(
      (r) => r.url().endsWith(`${path}/task-split-decisions`) && r.request().method() === 'POST',
    );
    await review.getByRole('button', { name: 'Reject scope split', exact: true }).click();
    expect((await stale).status()).toBe(412);
    await expect(review.getByRole('alert')).toContainText('Refresh');
    await review.getByRole('button', { name: 'Refresh case', exact: true }).click();
    await expect(coordinator.page.getByTestId('restoration-summary')).toContainText(
      '0 of 2 required tasks verified',
    );
    await expect(review.getByLabel('Scope split decision')).toHaveValue('REJECT');
    await expect(review.getByLabel('Scope split review reason')).toHaveValue(reason);
    const rejected = coordinator.page.waitForResponse(
      (r) => r.url().endsWith(`${path}/task-split-decisions`) && r.request().method() === 'POST',
    );
    await review.getByRole('button', { name: 'Reject scope split', exact: true }).click();
    expect((await rejected).status()).toBe(200);
    await expect(review).toContainText('rejected');
    detail = (await (await coordinator.page.request.get(path)).json()) as Schema['CaseDetail'];
    expect(detail.obligations).toHaveLength(2);
    expect(detail.obligations.find((o) => o.id === original)?.scopeReplaced).toBe(false);
    expect(detail.obligations.find((o) => o.id === original)?.requiredForRestoration).toBe(true);
    await officer.page.reload();
    await officer.page.getByTestId(`staff-case-${caseId}`).click();
    const card = officer.page.getByTestId(`obligation-${original}`);
    await expect(
      card.getByRole('button', { name: 'Accept part of this task', exact: true }),
    ).toBeVisible();
    await card
      .getByLabel('Work or decision summary')
      .fill('Fictional agency accepts the complete original work after rejection');
    await card.getByRole('button', { name: 'Accept task', exact: true }).click();
    await expect(card.getByRole('button', { name: 'Start work', exact: true })).toBeEnabled();
    await expect(card.getByTestId(`task-split-${requestId}`)).toContainText(reason);
  } finally {
    await coordinator.context.close();
    await officer.context.close();
  }
});

test('governed prerequisite additions recover acknowledgement loss and preserve verification gates', async ({
  page,
  browser,
}) => {
  test.setTimeout(120_000);
  await page.goto('/');
  await signIn(page, 'Rohan Mehta');
  const coordinator = await staffPage(browser, 'Kiran Shah');
  const officer = await staffPage(browser, 'City Works team');
  const verifier = await staffPage(browser, 'Neha Sen');
  const csrf = { 'x-jansetu-csrf': '1' };
  try {
    const { caseId, report } = await publicProgressFixture(page, coordinator.page);
    const path = `/api/authority/cases/${caseId}`;
    let detail = (await (await coordinator.page.request.get(path)).json()) as Schema['CaseDetail'];
    const first = detail.obligations[0].id;
    const originalProposal = {
      clientTaskId: crypto.randomUUID(),
      agencyId: '30000000-0000-4000-8000-000000000001',
      scope: 'PRIVATE finish the surface after the necessary earlier repair',
    };
    const proposed = await coordinator.page.request.post(`${path}/obligations`, {
      headers: { ...csrf, 'if-match': '"1"' },
      data: originalProposal,
    });
    expect(proposed.status()).toBe(201);
    const dependent = ((await proposed.json()) as Schema['TaskProposalResult']).id;
    await openPublicationReview(coordinator.page, caseId);
    const panel = coordinator.page.getByTestId(`prerequisite-amendments-${dependent}`);
    await panel.getByRole('button', { name: 'Add prerequisite tasks', exact: true }).click();
    await panel.getByRole('checkbox', { name: /Task 1 ·/ }).check();
    const reason =
      'PRIVATE coordinator reviewed that surface repair must follow earlier restoration';
    await panel.getByLabel('Prerequisite addition reason').fill(reason);
    await panel
      .getByRole('checkbox', { name: 'These requirements are necessary before work begins.' })
      .check();
    await coordinator.page.setViewportSize({ width: 320, height: 780 });
    await panel.scrollIntoViewIfNeeded();
    await expect
      .poll(() =>
        coordinator.page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
      )
      .toBe(true);
    await coordinator.page.screenshot({
      path: 'test-results/prerequisite-amendments-mobile-light.png',
    });
    await coordinator.page.getByRole('button', { name: 'Use dark theme', exact: true }).click();
    await panel.scrollIntoViewIfNeeded();
    await expect
      .poll(() =>
        coordinator.page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
      )
      .toBe(true);
    await coordinator.page.screenshot({
      path: 'test-results/prerequisite-amendments-mobile-dark.png',
    });
    const endpoint = `/api/authority/obligations/${dependent}/prerequisite-amendments`;
    let loseAck = true;
    let clientAmendmentId = '';
    let amendmentId = '';
    await coordinator.page.route(`**${endpoint}`, async (route) => {
      if (!loseAck) return route.continue();
      loseAck = false;
      clientAmendmentId = (route.request().postDataJSON() as Schema['PrerequisiteAmendmentInput'])
        .clientAmendmentId;
      const saved = await route.fetch();
      expect(saved.status()).toBe(201);
      amendmentId = ((await saved.json()) as Schema['PrerequisiteAmendmentResult']).id;
      await route.abort('failed');
    });
    await panel.getByRole('button', { name: 'Record prerequisite additions', exact: true }).click();
    await expect(panel.getByRole('alert')).toBeVisible();
    await expect(panel.getByLabel('Prerequisite addition reason')).toHaveValue(reason);
    await expect(panel.getByRole('checkbox', { name: /Task 1 ·/ })).toBeChecked();
    const retried = coordinator.page.waitForResponse(
      (r) => r.url().endsWith(endpoint) && r.request().method() === 'POST',
    );
    await panel.getByRole('button', { name: 'Record prerequisite additions', exact: true }).click();
    const retry = await retried;
    expect(retry.status()).toBe(201);
    expect(
      (retry.request().postDataJSON() as Schema['PrerequisiteAmendmentInput']).clientAmendmentId,
    ).toBe(clientAmendmentId);
    expect(((await retry.json()) as Schema['PrerequisiteAmendmentResult']).id).toBe(amendmentId);
    await expect(panel).toContainText('Prerequisites added by coordinator');
    await expect(panel).toContainText(reason);
    detail = (await (await coordinator.page.request.get(path)).json()) as Schema['CaseDetail'];
    expect(detail.prerequisiteAmendments).toHaveLength(1);
    expect(detail.version).toBe(3);
    expect(detail.obligations.find((o) => o.id === dependent)?.prerequisiteTaskIds).toEqual([
      first,
    ]);
    expect(
      detail.obligations.find((o) => o.id === first)?.availablePrerequisiteTaskIds,
    ).not.toContain(dependent);
    const originalRetry = await coordinator.page.request.post(`${path}/obligations`, {
      headers: { ...csrf, 'if-match': '"1"' },
      data: originalProposal,
    });
    expect(originalRetry.status()).toBe(201);
    expect(((await originalRetry.json()) as Schema['TaskProposalResult']).id).toBe(dependent);
    const staleAcceptance = await officer.page.request.post(
      `/api/authority/obligations/${dependent}/accept`,
      {
        headers: { ...csrf, 'if-match': '"1"' },
        data: { summary: 'Fictional stale agency acceptance before sequence review' },
      },
    );
    expect(staleAcceptance.status()).toBe(412);
    await officer.page.reload();
    await officer.page.getByTestId(`staff-case-${caseId}`).click();
    const dependentCard = officer.page.getByTestId(`obligation-${dependent}`);
    await expect(dependentCard).toContainText(reason);
    await expect(
      officer.page.getByRole('button', { name: 'Add prerequisite tasks', exact: true }),
    ).not.toBeVisible();
    const perform = async (id: string, labels: string[]) => {
      const card = officer.page.getByTestId(`obligation-${id}`);
      for (const label of labels) {
        await card
          .getByLabel('Work or decision summary')
          .fill('Fictional necessary restoration stage documented');
        await card.getByRole('button', { name: label, exact: true }).click();
        await expect(card.getByRole('button', { name: label, exact: true })).not.toBeVisible();
      }
    };
    const inspect = async (id: string) => {
      await verifier.page.reload();
      await verifier.page.getByTestId(`staff-case-${caseId}`).click();
      const card = verifier.page.getByTestId(`obligation-${id}`);
      await card
        .getByLabel('Independent inspection reason')
        .fill('Independent fictional inspection confirms necessary work restored');
      const response = verifier.page.waitForResponse(
        (r) =>
          r.url().endsWith(`${path}/verification-decisions`) && r.request().method() === 'POST',
      );
      await card.getByRole('button', { name: 'Record verification decision' }).click();
      expect((await response).status()).toBe(200);
      await officer.page.reload();
      await officer.page.getByTestId(`staff-case-${caseId}`).click();
    };
    await perform(dependent, ['Accept task']);
    await expect(
      dependentCard.getByRole('button', { name: 'Start work', exact: true }),
    ).toBeDisabled();
    await perform(first, ['Accept task', 'Start work', 'Claim completion']);
    await expect(
      dependentCard.getByRole('button', { name: 'Start work', exact: true }),
    ).toBeDisabled();
    await inspect(first);
    await expect(
      dependentCard.getByRole('button', { name: 'Start work', exact: true }),
    ).toBeEnabled();
    await perform(dependent, ['Start work', 'Claim completion']);
    await inspect(dependent);
    detail = (await (await coordinator.page.request.get(path)).json()) as Schema['CaseDetail'];
    expect(detail.state).toBe('RESOLVED');
    expect(detail.firstReportedAt).toBe(report.receivedAt);
    const own = (await (
      await page.request.get(`/api/my-reports/${report.id}`)
    ).json()) as Schema['ReportProgress'];
    expect(own.state).toBe('VERIFIED');
    for (const secret of [reason, amendmentId, clientAmendmentId, first, dependent, caseId])
      expect(JSON.stringify(own)).not.toContain(secret);
  } finally {
    await coordinator.context.close();
    await officer.context.close();
    await verifier.context.close();
  }
});

test('governed prerequisite additions keep stale drafts and reject readiness cycles', async ({
  page,
  browser,
}) => {
  test.setTimeout(90_000);
  await page.goto('/');
  await signIn(page, 'Rohan Mehta');
  const coordinator = await staffPage(browser, 'Kiran Shah');
  const officer = await staffPage(browser, 'City Works team');
  const csrf = { 'x-jansetu-csrf': '1' };
  try {
    const { caseId } = await publicProgressFixture(page, coordinator.page);
    const path = `/api/authority/cases/${caseId}`;
    let detail = (await (await coordinator.page.request.get(path)).json()) as Schema['CaseDetail'];
    const first = detail.obligations[0].id;
    const propose = async (scope: string, version: number) => {
      const response = await coordinator.page.request.post(`${path}/obligations`, {
        headers: { ...csrf, 'if-match': `"${version}"` },
        data: {
          clientTaskId: crypto.randomUUID(),
          agencyId: '30000000-0000-4000-8000-000000000001',
          scope,
        },
      });
      expect(response.status()).toBe(201);
      return ((await response.json()) as Schema['TaskProposalResult']).id;
    };
    const second = await propose('PRIVATE dependent surface assessment to sequence', 1);
    const third = await propose('PRIVATE additional prerequisite infrastructure check', 2);
    await openPublicationReview(coordinator.page, caseId);
    const panel = coordinator.page.getByTestId(`prerequisite-amendments-${second}`);
    await panel.getByRole('button', { name: 'Add prerequisite tasks', exact: true }).click();
    await panel.getByRole('checkbox', { name: /Task 1 ·/ }).check();
    const reason = 'PRIVATE reviewed first prerequisite must precede dependent work';
    await panel.getByLabel('Prerequisite addition reason').fill(reason);
    await panel
      .getByRole('checkbox', { name: 'These requirements are necessary before work begins.' })
      .check();
    const endpoint = `/api/authority/obligations/${second}/prerequisite-amendments`;
    const competing = await coordinator.page.request.post(endpoint, {
      headers: { ...csrf, 'if-match': '"1"' },
      data: {
        clientAmendmentId: crypto.randomUUID(),
        addedPrerequisiteTaskIds: [third],
        reason: 'PRIVATE separate coordinator addition requires third task',
        reviewed: true,
      },
    });
    expect(competing.status()).toBe(201);
    const stale = coordinator.page.waitForResponse(
      (r) => r.url().endsWith(endpoint) && r.request().method() === 'POST',
    );
    await panel.getByRole('button', { name: 'Record prerequisite additions', exact: true }).click();
    expect((await stale).status()).toBe(412);
    await expect(panel.getByRole('alert')).toContainText('Refresh');
    await panel.getByRole('button', { name: 'Refresh case', exact: true }).click();
    await expect(panel).toContainText('PRIVATE separate coordinator addition requires third task');
    await expect(panel.getByLabel('Prerequisite addition reason')).toHaveValue(reason);
    await expect(panel.getByRole('checkbox', { name: /Task 1 ·/ })).toBeChecked();
    const saved = coordinator.page.waitForResponse(
      (r) => r.url().endsWith(endpoint) && r.request().method() === 'POST',
    );
    await panel.getByRole('button', { name: 'Record prerequisite additions', exact: true }).click();
    expect((await saved).status()).toBe(201);
    await expect(panel).toContainText(reason);
    detail = (await (await coordinator.page.request.get(path)).json()) as Schema['CaseDetail'];
    expect(detail.prerequisiteAmendments).toHaveLength(2);
    expect(
      detail.obligations
        .find((o) => o.id === second)
        ?.prerequisiteTaskIds.slice()
        .sort(),
    ).toEqual([first, third].sort());
    // A competing coordinator records the draft's last eligible choice. Refresh
    // must retain that visible selection and the draft even after it is unchecked.
    const overlapPanel = coordinator.page.getByTestId(`prerequisite-amendments-${third}`);
    await overlapPanel.getByRole('button', { name: 'Add prerequisite tasks', exact: true }).click();
    await overlapPanel.getByRole('checkbox', { name: /Task 1 ·/ }).check();
    const overlapReason = 'PRIVATE draft with an overlapping prerequisite must remain recoverable';
    await overlapPanel.getByLabel('Prerequisite addition reason').fill(overlapReason);
    const confirmation = overlapPanel.getByRole('checkbox', {
      name: 'These requirements are necessary before work begins.',
    });
    await confirmation.check();
    const overlapEndpoint = `/api/authority/obligations/${third}/prerequisite-amendments`;
    expect(
      (
        await coordinator.page.request.post(overlapEndpoint, {
          headers: { ...csrf, 'if-match': '"1"' },
          data: {
            clientAmendmentId: crypto.randomUUID(),
            addedPrerequisiteTaskIds: [first],
            reason: 'PRIVATE another coordinator recorded the last compatible choice',
            reviewed: true,
          },
        })
      ).status(),
    ).toBe(201);
    const overlapStale = coordinator.page.waitForResponse(
      (r) => r.url().endsWith(overlapEndpoint) && r.request().method() === 'POST',
    );
    const overlapSubmit = overlapPanel.getByRole('button', {
      name: 'Record prerequisite additions',
      exact: true,
    });
    await overlapSubmit.click();
    expect((await overlapStale).status()).toBe(412);
    await overlapPanel.getByRole('button', { name: 'Refresh case', exact: true }).click();
    await expect(overlapPanel).toContainText('Already recorded.');
    await expect(overlapPanel).toContainText('No compatible additional prerequisites remain.');
    await expect(overlapPanel.getByLabel('Prerequisite addition reason')).toHaveValue(
      overlapReason,
    );
    await expect(confirmation).toBeChecked();
    const unavailableChoice = overlapPanel.getByRole('checkbox', { name: /Task 1 ·/ });
    await expect(unavailableChoice).toBeChecked();
    await expect(overlapSubmit).toBeDisabled();
    // Deselecting removes this unavailable row; assert the resulting disappearance
    // instead of asking uncheck() to re-read an input that no longer exists.
    await unavailableChoice.click();
    await expect(unavailableChoice).toHaveCount(0);
    await expect(overlapPanel.getByLabel('Prerequisite addition reason')).toHaveValue(
      overlapReason,
    );
    await expect(confirmation).toBeChecked();
    await expect(overlapSubmit).toBeDisabled();
    const overlapToggle = overlapPanel.getByRole('button', {
      name: 'Add prerequisite tasks',
      exact: true,
    });
    await overlapToggle.click();
    await expect(overlapPanel.getByLabel('Prerequisite addition reason')).toHaveCount(0);
    await overlapToggle.click();
    await expect(overlapPanel.getByLabel('Prerequisite addition reason')).toHaveValue(
      overlapReason,
    );
    await expect(confirmation).toBeChecked();
    detail = (await (await coordinator.page.request.get(path)).json()) as Schema['CaseDetail'];
    expect(detail.prerequisiteAmendments).toHaveLength(3);
    expect(detail.obligations.find((o) => o.id === third)?.canAmendPrerequisites).toBe(true);
    expect(detail.obligations.find((o) => o.id === third)?.availablePrerequisiteTaskIds).toEqual(
      [],
    );
    const cycle = await coordinator.page.request.post(
      `/api/authority/obligations/${first}/prerequisite-amendments`,
      {
        headers: { ...csrf, 'if-match': '"1"' },
        data: {
          clientAmendmentId: crypto.randomUUID(),
          addedPrerequisiteTaskIds: [second],
          reason: 'PRIVATE proposed but circular prerequisite addition',
          reviewed: true,
        },
      },
    );
    expect(cycle.status()).toBe(422);
    expect((await cycle.json()).code).toBe('TASK_PREREQUISITE_CYCLE');
    expect(
      detail.obligations.find((o) => o.id === first)?.availablePrerequisiteTaskIds,
    ).not.toContain(second);
    const forged = await officer.page.request.post(endpoint, {
      headers: { ...csrf, 'if-match': '"3"' },
      data: {
        clientAmendmentId: crypto.randomUUID(),
        addedPrerequisiteTaskIds: [first],
        reason: 'PRIVATE agency cannot change the agreed sequence',
        reviewed: true,
      },
    });
    expect(forged.status()).toBe(403);
    await expect(coordinator.page.getByTestId('restoration-summary')).toContainText(
      '0 of 3 required tasks verified',
    );
  } finally {
    await coordinator.context.close();
    await officer.context.close();
  }
});
