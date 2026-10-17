import { expect, test, type Page } from '@playwright/test';
import { BASE_URL } from './feeds.mjs';

// The feed's columns (cols= in the URL, cols: in the "/" bar, cols in a saved
// view), at the desktop and the mobile viewport. Both projects share one
// daemon, so the views carry the project's name and are removed again.

const isMobile = (page: Page) => (page.viewportSize()?.width ?? 1000) < 720;
const project = () => test.info().project.name;
const headers = { Origin: BASE_URL };

const row = (page: Page, title: string) => page.locator('[role=row]').filter({ hasText: title });
const search = (page: Page) => page.getByTestId('search');
const RUST = 'Rust for Linux status';

/** Opens the feed and waits for a row with this text (a title, unless the titles are hidden). */
async function open(page: Page, path = '/', ready = 'Linux 6.18 released') {
	await page.goto(path);
	await expect(page.getByTestId('status').locator('[data-status]')).toHaveAttribute('data-status', 'connected');
	await expect(row(page, ready)).toBeVisible();
}

async function query(page: Page, text: string) {
	if (isMobile(page)) await search(page).tap();
	else await page.keyboard.press('/');
	await search(page).fill(text);
	await page.keyboard.press('Enter');
}

test.afterEach(async ({ page }) => {
	const res = await page.request.get('/api/views');
	for (const v of ((await res.json()) as { views: { id: string; name: string }[] }).views) {
		if (v.name.endsWith(` ${project()}`)) await page.request.delete(`/api/views/${v.id}`, { headers, data: {} });
	}
});

test('columns: the URL picks the cells, and the bar shows cols:', async ({ page }) => {
	await open(page, '/?cols=title,source');
	const r = row(page, RUST);
	await expect(r).toContainText('[ALP]');
	await expect(r).toContainText(RUST);
	await expect(r).not.toContainText('[linux]');
	await expect(r).not.toContainText('lwn.net');
	await expect(r).not.toContainText('An overview');
	await expect(r.locator('.date')).toHaveCount(0);
	await expect(search(page)).toHaveValue('cols:source,title');

	// Without a title the description takes its place, on a phone too (where
	// it is otherwise never shown).
	const overview = 'An overview of where Rust support stands.';
	await open(page, '/?cols=desc,source', overview);
	await expect(row(page, overview).locator('.title')).toHaveCount(0);
	await expect(row(page, overview)).not.toContainText(RUST);
});

test('columns: a change of columns re-renders without reloading, and a view keeps them', async ({ page }) => {
	const name = `Cols ${project()}`;
	await open(page);
	await expect(row(page, RUST)).toContainText('lwn.net');

	// Columns alone: the rows change, the stream and the list are not reloaded.
	let feedRequests = 0;
	page.on('request', (req) => {
		const path = new URL(req.url()).pathname;
		if (path === '/api/stream' || path === '/api/items') feedRequests++;
	});
	await query(page, 'cols:date,title');
	await expect(page).toHaveURL(/cols=date%2Ctitle/);
	await expect(row(page, RUST)).not.toContainText('lwn.net');
	await expect(row(page, RUST)).not.toContainText('[ALP]');
	await expect(row(page, RUST).locator('.date').first()).toBeAttached();
	expect(feedRequests).toBe(0);

	// Saved into a view with the rest of the filter.
	if (isMobile(page)) {
		await page.getByTestId('view-save').tap();
		await page.keyboard.type(name);
	} else {
		await page.keyboard.press(':');
		await page.keyboard.type(`save ${name}`);
	}
	await page.keyboard.press('Enter');
	const tab = page.getByTestId('view-tab').filter({ hasText: name });
	await expect(tab).toHaveAttribute('aria-current', 'page');
	await expect
		.poll(async () => {
			const views = ((await (await page.request.get('/api/views')).json()) as { views: { name: string; filter: { cols?: string[] } }[] }).views;
			return views.find((v) => v.name === name)?.filter.cols;
		})
		.toEqual(['date', 'title']);

	// The unfiltered feed shows every column; the tab brings the view's back.
	await page.getByTestId('view-tab-all').click();
	await expect(row(page, RUST)).toContainText('lwn.net');
	await tab.click();
	await expect(row(page, RUST)).not.toContainText('lwn.net');
	await expect(page.getByTestId('view-modified')).toHaveCount(0);

	// Changing only the columns marks the view modified.
	await query(page, 'cols:title');
	await expect(page.getByTestId('view-modified')).toBeVisible();
	await expect(row(page, RUST).locator('.date')).toHaveCount(0);
});
