import { expect, test, type Page } from '@playwright/test';
import { BASE_URL } from './feeds.mjs';

// Archiving an item and the /archives page. The archive downloads themselves
// are pinned by the nyttig-api tests (headers) and need a real archiver for
// an end-to-end check.

const isMobile = (page: Page) => (page.viewportSize()?.width ?? 1000) < 720;
const row = (page: Page, title: string) => page.locator('[role=row]').filter({ hasText: title });

const TITLE = 'Go 1.26.8 security release';

async function removeArchives(page: Page) {
	const res = await page.request.get('/api/archives?limit=200');
	const body = (await res.json()) as { archives?: { id: string; title?: string }[] };
	for (const a of body.archives ?? []) {
		if (a.title === TITLE) await page.request.delete(`/api/archives/${a.id}`, { headers: { Origin: BASE_URL }, data: {} });
	}
}

test.describe('archive', () => {
	test.beforeEach(({ page }) => removeArchives(page));
	test.afterEach(({ page }) => removeArchives(page));

	test('archive an item, see its failed status, list and remove it', async ({ page }) => {
		await page.goto('/');
		await expect(page.getByTestId('status').locator('[data-status]')).toHaveAttribute('data-status', 'connected');
		const r = row(page, TITLE);
		await expect(r).toBeVisible();

		// Select and expand the row. Touch screens have a button, not the b key.
		if (isMobile(page)) {
			await r.tap();
			await page.getByTestId('archive-button').tap();
		} else {
			await r.click();
			await page.keyboard.press('b');
			await page.keyboard.press(' ');
		}

		// The test daemon has no real archiver, so the archive fails; the
		// badge follows through item_update.
		const badge = r.getByTestId('archive-badge');
		await expect(badge).toHaveAttribute('data-status', 'failed');
		const detail = page.getByTestId('archive-detail');
		await expect(detail).toContainText('archive=failed');
		await expect(detail).toContainText('error=');
		await expect(page.getByTestId('archive-download')).toHaveCount(0);

		// Nothing was saved, so there is nothing to download.
		const list = await (await page.request.get('/api/archives')).json();
		const id = list.archives.find((a: { title?: string }) => a.title === TITLE).id as string;
		const file = await page.request.get(`/api/archives/${id}/file`);
		expect(file.status()).toBe(400);
		expect(file.headers()['content-disposition'] ?? '').not.toContain('attachment');

		// The archives page lists it; a status filter hides it.
		await page.goto('/archives');
		const ar = page.getByTestId('archives-list').locator('[role=row]').filter({ hasText: TITLE });
		await expect(ar).toBeVisible();
		await expect(ar.getByTestId('archive-status')).toHaveAttribute('data-status', 'failed');
		await expect(ar.getByTestId('archive-why')).not.toBeEmpty();

		// Remove it (y on desktop, the button on mobile).
		await ar.click();
		if (isMobile(page)) {
			await page.getByTestId('tool-delete').tap();
			await page.getByTestId('confirm-delete').tap();
		} else {
			await page.keyboard.press('x');
			await page.keyboard.press('y');
		}
		await expect(ar).toHaveCount(0);

		await page.goto('/');
		await expect(row(page, TITLE)).toBeVisible();
		await expect(row(page, TITLE).getByTestId('archive-badge')).toHaveCount(0);
	});
});
