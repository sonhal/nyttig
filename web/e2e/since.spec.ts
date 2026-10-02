import { expect, test, type Page } from '@playwright/test';
import { BASE_URL, FEEDS_URL } from './feeds.mjs';

// The date window ("since:7d") against the real daemon, at the desktop
// viewport (keys, chips) and the mobile one (taps, the filter sheet). Each
// test adds a source whose feed has items 1 hour, 2 days, 10 days and 60 days
// old (feeds.mjs: /dated/<name>.xml). Both projects share one daemon, so the
// source name carries the project, and it is removed again after each test.

const isMobile = (page: Page) => (page.viewportSize()?.width ?? 1000) < 720;
const project = () => test.info().project.name;
const headers = { Origin: BASE_URL };
const name = () => `since-${project()}`;

interface ViewJSON {
	id: string;
	name: string;
	filter: { q?: string; source?: string; tag?: string; sort?: string; since?: string; unviewed?: boolean };
	favorite?: boolean;
}

const apiViews = async (page: Page): Promise<ViewJSON[]> =>
	((await (await page.request.get('/api/views')).json()) as { views: ViewJSON[] }).views;

const search = (page: Page) => page.getByTestId('search');
const rows = (page: Page) => page.locator('[role=row]');
const tab = (page: Page, text: string) => page.getByTestId('view-tab').filter({ hasText: text });

let sourceId = '';

/** Adds this project's source and waits until the daemon has fetched all four items. */
async function addDatedSource(page: Page) {
	const res = await page.request.post('/api/sources', {
		headers,
		data: { name: name(), url: `${FEEDS_URL}/dated/${name()}.xml`, refresh_sec: 3600 }
	});
	expect(res.status()).toBe(201);
	sourceId = ((await res.json()) as { id: string }).id;
	await expect
		.poll(async () => ((await (await page.request.get(`/api/items?source=${sourceId}`)).json()) as { total?: number }).total)
		.toBe(4);
}

test.afterEach(async ({ page }) => {
	for (const v of await apiViews(page)) {
		if (v.name.endsWith(` ${project()}`)) await page.request.delete(`/api/views/${v.id}`, { headers, data: {} });
	}
	if (sourceId) await page.request.delete(`/api/sources/${sourceId}`, { headers, data: {} });
	sourceId = '';
});

async function openFeed(page: Page, path = '/') {
	await page.goto(path);
	await expect(page.getByTestId('status').locator('[data-status]')).toHaveAttribute('data-status', 'connected');
	await expect(page.getByTestId('view-tabs')).toBeVisible();
}

async function query(page: Page, text: string) {
	if (isMobile(page)) await search(page).tap();
	else await page.keyboard.press('/');
	await search(page).fill(text);
	await page.keyboard.press('Enter');
}

async function command(page: Page, text: string) {
	if (isMobile(page) && text.startsWith('save')) {
		await page.getByTestId('view-save').tap();
		await page.keyboard.type(text.slice('save'.length).trimStart());
	} else {
		await page.keyboard.press(':');
		await page.keyboard.type(text);
	}
	await page.keyboard.press('Enter');
	await expect(page.getByTestId('command-line')).toHaveCount(0);
}

async function goToViews(page: Page) {
	if (isMobile(page)) {
		await page.getByTestId('open-filters').tap();
		await page.getByTestId('manage-views').tap();
	} else {
		await page.keyboard.press(':');
		await page.keyboard.type('views');
		await page.keyboard.press('Enter');
	}
	await expect(page).toHaveURL(/\/views$/);
	await expect(page.getByTestId('views-list')).toBeVisible();
}

async function backToFeed(page: Page) {
	if (isMobile(page)) await page.getByTestId('nav-feed').tap();
	else await page.keyboard.press('q');
	await expect(page.getByTestId('feed')).toBeVisible();
}

test('since: a window in the query, saved as a view, restored by its tab', async ({ page }) => {
	const viewName = `Week ${project()}`;
	await addDatedSource(page);
	await openFeed(page);

	// since:7d shows the items from the last week: the hour-old and the 2-day-old one.
	await query(page, `src:${name()} since:7d`);
	await expect(page).toHaveURL(/since=7d/);
	await expect(rows(page)).toHaveCount(2);
	await expect(rows(page).filter({ hasText: `${name()} hour` })).toHaveCount(1);
	await expect(rows(page).filter({ hasText: `${name()} 2 days` })).toHaveCount(1);
	await expect(search(page)).toHaveValue(`src:${name()} since:7d`);

	// A window that nothing falls in says so.
	await query(page, `src:${name()} since:1h`);
	await expect(page.getByTestId('empty-window')).toHaveText('no items in the last 1h');
	await expect(rows(page)).toHaveCount(0);

	// A bad window is explained and not applied.
	await query(page, `src:${name()} since:soon`);
	await expect(page.getByTestId('query-error')).toContainText('not a window');
	await expect(page).toHaveURL(/since=1h/);
	await page.keyboard.press('Escape');

	// :save keeps the window with the rest of the filter.
	await query(page, `src:${name()} since:7d`);
	await expect(rows(page)).toHaveCount(2);
	await command(page, `save ${viewName}`);
	await expect(tab(page, viewName)).toHaveAttribute('aria-current', 'page');
	await expect(page.getByTestId('view-modified')).toHaveCount(0);
	const [created] = (await apiViews(page)).filter((v) => v.name === viewName);
	expect(created).toMatchObject({ favorite: true, filter: { source: sourceId, since: '7d' } });

	// 0 is the unfiltered feed, without the window; the view's tab brings it back.
	if (isMobile(page)) await page.getByTestId('view-tab-all').tap();
	else await page.keyboard.press('0');
	await expect(page).not.toHaveURL(/since=/);
	if (isMobile(page)) await tab(page, viewName).tap();
	else await page.keyboard.press('1');
	await expect(page).toHaveURL(/since=7d/);
	await expect(rows(page)).toHaveCount(2);

	// Widening the window modifies the view; the tab link resets it.
	await query(page, `src:${name()} since:1mo`);
	await expect(rows(page)).toHaveCount(3);
	await expect(rows(page).filter({ hasText: `${name()} 10 days` })).toHaveCount(1);
	await expect(rows(page).filter({ hasText: `${name()} 60 days` })).toHaveCount(0);
	await expect(page.getByTestId('view-modified')).toBeVisible();
	await expect(tab(page, viewName)).toContainText('*');
	if (isMobile(page)) await tab(page, viewName).tap();
	else await page.keyboard.press('1');
	await expect(page.getByTestId('view-modified')).toHaveCount(0);
	await expect(rows(page)).toHaveCount(2);

	// The management page shows the window in the view's filter.
	await goToViews(page);
	const row = page.getByTestId('views-list').locator('[role=row]').filter({ hasText: viewName });
	await expect(row.getByTestId('view-filter')).toHaveText(`src:${name()} since:7d`);
	await backToFeed(page);

	// Respelling the window in the query text edits the stored view.
	await query(page, `src:${name()} since:30d`);
	await command(page, 'save');
	await expect(page.getByTestId('view-modified')).toHaveCount(0);
	await expect.poll(async () => (await apiViews(page)).find((v) => v.id === created!.id)?.filter.since).toBe('30d');
	await expect(rows(page)).toHaveCount(3);
});

test('since: set without typing, and the API takes a cutoff', async ({ page }) => {
	await addDatedSource(page);
	await openFeed(page, `/?source=${sourceId}`);
	await expect(rows(page)).toHaveCount(4);

	if (isMobile(page)) {
		await page.getByTestId('open-filters').tap();
		await page.getByTestId('sheet-since').selectOption('7d');
		await expect(page).toHaveURL(/since=7d/);
		await page.getByRole('button', { name: 'done' }).tap();
	} else {
		await expect(page.getByTestId('chip-since')).toContainText('any');
		await page.getByTestId('chip-since').click(); // 24h
		await expect(page).toHaveURL(/since=24h/);
		await expect(rows(page)).toHaveCount(1);
		await page.getByTestId('chip-since').click(); // 7d
		await expect(page).toHaveURL(/since=7d/);
		await expect(page.getByTestId('chip-since')).toContainText('7d');
	}
	await expect(rows(page)).toHaveCount(2);

	// A bookmark with a window the app does not know falls back to any time.
	await openFeed(page, `/?source=${sourceId}&since=1m`);
	await expect(rows(page)).toHaveCount(4);

	// GET /api/items takes the cutoff in unix seconds; a window is not a parameter.
	const after = Math.floor(Date.now() / 1000) - 7 * 24 * 3600;
	const body = (await (await page.request.get(`/api/items?source=${sourceId}&after=${after}`)).json()) as { total?: number };
	expect(body.total).toBe(2);
	for (const bad of ['after=-1', 'after=7d', 'after=1.5']) {
		expect((await page.request.get(`/api/items?${bad}`)).status()).toBe(400);
	}
	expect((await page.request.get('/api/stream?after=soon')).status()).toBe(400);
});

test('since: items older than the window are not pushed into the feed', async ({ page }) => {
	await openFeed(page, '/?since=24h');
	// The new source's backlog is pushed to the open stream; only its hour-old item is in the window.
	await addDatedSource(page);
	await expect(rows(page).filter({ hasText: `${name()} hour` })).toHaveCount(1);
	await expect(rows(page).filter({ hasText: /days/ }).filter({ hasText: name() })).toHaveCount(0);
});
