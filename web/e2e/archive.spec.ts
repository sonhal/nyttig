import { expect, test, type Page } from '@playwright/test';
import { BASE_URL, FEEDS_URL } from './feeds.mjs';

// Archiving items through the real archiver: a page on the feed server with a
// stylesheet, an image and a script is saved and downloaded, and a 404 fails
// and can be removed. Both projects share one daemon, so the source and its
// archives are named after the project and removed again (archives outlive
// their source).

const project = () => test.info().project.name;
const headers = { Origin: BASE_URL };
const isMobile = (page: Page) => (page.viewportSize()?.width ?? 1000) < 720;
const row = (page: Page, title: string) => page.locator('[role=row]').filter({ hasText: title });

let sourceId = '';

async function removeArchives(page: Page, name: string) {
	const res = await page.request.get('/api/archives?limit=200');
	const body = (await res.json()) as { archives?: { id: string; title?: string }[] };
	for (const a of body.archives ?? []) {
		if (a.title?.startsWith(name)) await page.request.delete(`/api/archives/${a.id}`, { headers, data: {} });
	}
}

test.beforeEach(async ({ page }) => {
	const name = `arch-${project()}`;
	await removeArchives(page, name);
	const res = await page.request.post('/api/sources', {
		headers,
		data: { name, url: `${FEEDS_URL}/articles/${name}.xml`, refresh_sec: 3600 }
	});
	expect(res.status(), await res.text()).toBeLessThan(300);
	sourceId = ((await res.json()) as { id: string }).id;
	await expect
		.poll(async () => {
			const items = await (await page.request.get(`/api/items?source=${sourceId}`)).json();
			return (items.items ?? []).length;
		})
		.toBe(2);
});

test.afterEach(async ({ page }) => {
	if (sourceId) await page.request.delete(`/api/sources/${sourceId}`, { headers, data: {} });
	sourceId = '';
	await removeArchives(page, `arch-${project()}`);
});

async function archiveRow(page: Page, title: string) {
	await page.goto(`/?source=${sourceId}`);
	await expect(page.getByTestId('status').locator('[data-status]')).toHaveAttribute('data-status', 'connected');
	const r = row(page, title);
	await expect(r).toBeVisible();
	// Touch screens have a button, not the b key.
	if (isMobile(page)) {
		await r.tap();
		await page.getByTestId('archive-button').tap();
	} else {
		// Clicking the selected row expands it; space expands any other.
		await r.click();
		await page.keyboard.press('b');
		if (!(await page.getByTestId('archive-detail').isVisible())) await page.keyboard.press(' ');
	}
	await expect(page.getByTestId('archive-detail')).toBeVisible();
	return r;
}

test('archive a page and download a self-contained, inert copy', async ({ page }) => {
	const title = `arch-${project()} article`;
	const r = await archiveRow(page, title);

	// The badge follows through item_update.
	await expect(r.getByTestId('archive-badge')).toHaveAttribute('data-status', 'done');
	const link = page.getByTestId('archive-download');
	await expect(link).toBeVisible();

	const file = await page.request.get((await link.getAttribute('href'))!);
	expect(file.status()).toBe(200);
	const h = file.headers();
	expect(h['content-disposition']).toMatch(/^attachment; filename="?[\w.-]+\.html"?$/);
	expect(h['content-type']).toBe('application/octet-stream');
	expect(h['content-security-policy']).toContain('sandbox');
	expect(h['x-content-type-options']).toBe('nosniff');

	const html = await file.text();
	expect(html).toContain(`arch-${project()} article`);
	expect(html).toContain('data:image/png;base64,');
	expect(html).toContain('Content-Security-Policy');
	expect(html).not.toMatch(/<script/i);
	expect(html).not.toMatch(/onload=/i);
	expect(html).not.toContain('javascript:');
	expect(html).not.toContain(`${FEEDS_URL}/article/arch-${project()}.png`);
});

test('a page that cannot be fetched fails, has no download, and can be removed', async ({ page }) => {
	const title = `arch-${project()} gone`;
	const r = await archiveRow(page, title);

	await expect(r.getByTestId('archive-badge')).toHaveAttribute('data-status', 'failed');
	const detail = page.getByTestId('archive-detail');
	await expect(detail).toContainText('archive=failed');
	await expect(detail).toContainText('HTTP 404');
	await expect(page.getByTestId('archive-download')).toHaveCount(0);

	const list = await (await page.request.get('/api/archives')).json();
	const id = list.archives.find((a: { title?: string }) => a.title === title).id as string;
	const file = await page.request.get(`/api/archives/${id}/file`);
	expect(file.status()).toBe(400);
	expect(file.headers()['content-disposition'] ?? '').not.toContain('attachment');

	// The archives page lists it.
	await page.goto('/archives');
	const ar = page.getByTestId('archives-list').locator('[role=row]').filter({ hasText: title });
	await expect(ar).toBeVisible();
	await expect(ar.getByTestId('archive-status')).toHaveAttribute('data-status', 'failed');
	await expect(ar.getByTestId('archive-why')).toContainText('HTTP 404');

	// Remove it (x then y on desktop, the buttons on mobile).
	await ar.click();
	if (isMobile(page)) {
		await page.getByTestId('tool-delete').tap();
		await page.getByTestId('confirm-delete').tap();
	} else {
		await page.keyboard.press('x');
		await page.keyboard.press('y');
	}
	await expect(ar).toHaveCount(0);

	await page.goto(`/?source=${sourceId}`);
	await expect(row(page, title)).toBeVisible();
	await expect(row(page, title).getByTestId('archive-badge')).toHaveCount(0);
});
