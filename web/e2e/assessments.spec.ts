import { expect, test, type Locator, type Page } from '@playwright/test';
import { BASE_URL, FEEDS_URL } from './feeds.mjs';

// Assessments against the real daemon, at the desktop viewport (keys) and the
// mobile one (taps): a score arriving live as a chip, the score: filter and
// sort:score in the query bar, a saved view that keeps the score sort, and
// what deleting the assessor does to that view. Both projects share one
// daemon, so every name carries the project and everything created is removed
// again, even when a test fails.

const isMobile = (page: Page) => (page.viewportSize()?.width ?? 1000) < 720;
const project = () => test.info().project.name;
const headers = { Origin: BASE_URL };

const search = (page: Page) => page.getByTestId('search');
const rows = (page: Page) => page.locator('[role=row]');
const titles = async (page: Page) => rows(page).locator('.title').allTextContents();
const tab = (page: Page, name: string) => page.getByTestId('view-tab').filter({ hasText: name });

interface Fixture {
	sourceId: string;
	slug: string;
	/** Item IDs, 1 is the newest. */
	items: string[];
	title: (n: number) => string;
	assessorId: string;
	assessor: string;
}

let fx: Fixture | undefined;

/** A source with three items, and an assessor. */
async function setup(page: Page): Promise<Fixture> {
	const slug = `asx-${project()}`;
	const res = await page.request.post('/api/sources', {
		headers,
		data: { name: `Bulk ${slug}`, url: `${FEEDS_URL}/bulk/${slug}.xml?n=3`, refresh_sec: 3600 }
	});
	expect(res.status()).toBe(201);
	const sourceId = ((await res.json()) as { id: string }).id;
	await expect
		.poll(async () => ((await (await page.request.get(`/api/items?source=${sourceId}&limit=1`)).json()) as { total?: number }).total, {
			timeout: 20_000
		})
		.toBe(3);
	const list = (await (await page.request.get(`/api/items?source=${sourceId}&limit=10`)).json()) as { items: { id: string }[] };
	const assessor = `claude-${project()}`;
	const a = await page.request.post('/api/assessors', { headers, data: { name: assessor, description: 'test scale, 0-1', color: '#D97757' } });
	expect(a.status()).toBe(201);
	return {
		sourceId,
		slug,
		items: list.items.map((i) => i.id),
		title: (n) => `${slug} item ${n}`,
		assessorId: ((await a.json()) as { id: string }).id,
		assessor
	};
}

async function put(page: Page, item: string, body: Record<string, unknown>) {
	const res = await page.request.put(`/api/items/${item}/assessments`, { headers, data: body });
	expect(res.status(), await res.text()).toBe(200);
}

async function open(page: Page, path: string) {
	await page.goto(path);
	await expect(page.getByTestId('status').locator('[data-status]')).toHaveAttribute('data-status', 'connected');
	await expect(page.getByTestId('view-tabs')).toBeVisible();
}

/** Applies a query in the "/" bar (a tap on the field on mobile). */
async function query(page: Page, text: string) {
	if (isMobile(page)) await search(page).tap();
	else await page.keyboard.press('/');
	await search(page).fill(text);
	await page.keyboard.press('Enter');
}

/** ":save <name>" (the "+ save" tab starts the line with "save " on mobile). */
async function saveView(page: Page, name: string) {
	if (isMobile(page)) {
		await page.getByTestId('view-save').tap();
		await page.keyboard.type(name);
	} else {
		await page.keyboard.press(':');
		await page.keyboard.type('save ' + name);
	}
	await page.keyboard.press('Enter');
	await expect(page.getByTestId('command-line')).toHaveCount(0);
}

async function openTab(page: Page, name: string, key: string) {
	if (isMobile(page)) await tab(page, name).tap();
	else await page.keyboard.press(key);
}

test.beforeEach(async ({ page }) => {
	await page.goto('/');
	fx = await setup(page);
});

test.afterEach(async ({ page }) => {
	// Whatever the test left: this project's views, assessor and source.
	const views = ((await (await page.request.get('/api/views')).json()) as { views: { id: string; name: string }[] }).views;
	for (const v of views) {
		if (v.name.endsWith(` ${project()}`)) await page.request.delete(`/api/views/${v.id}`, { headers, data: {} });
	}
	if (fx) {
		await page.request.delete(`/api/assessors/${fx.assessorId}`, { headers, data: {} });
		await page.request.delete(`/api/sources/${fx.sourceId}`, { headers, data: {} });
	}
	fx = undefined;
});

test('assessments: a score shows up live as a chip, and the detail lists it as text', async ({ page }) => {
	const f = fx!;
	await open(page, `/?source=${f.sourceId}`);
	const row1 = rows(page).filter({ has: page.locator('.title', { hasText: new RegExp(`^${f.title(1)}$`) }) });
	await expect(row1).toBeVisible();
	await expect(page.getByTestId('score-chip')).toHaveCount(0);

	// Scored through the API while the page is open: the chip appears.
	await put(page, f.items[0]!, { assessor: f.assessorId, score: 0.9, note: '<b>bold</b> <img src=x onerror="window.__pwned=1"> critical' });
	await expect(row1.getByTestId('score-chip')).toContainText(`${f.assessor} 0.9`);
	// Scored again: the same chip, new value.
	await put(page, f.items[0]!, { assessor: f.assessorId, score: 0.35, note: '<b>bold</b> <img src=x onerror="window.__pwned=1"> critical' });
	await expect(row1.getByTestId('score-chip')).toHaveText(new RegExp(`\\[${f.assessor} 0\\.35\\]`));
	await expect(page.getByTestId('score-chip')).toHaveCount(1);
	// A score of 0 is a score.
	await put(page, f.items[1]!, { assessor: f.assessorId, score: 0 });
	await expect(page.getByTestId('score-chip').filter({ hasText: `${f.assessor} 0]` })).toHaveCount(1);

	// The detail lists the assessment; the note is text, not markup.
	// The newest row is the one selected when the feed opens.
	if (isMobile(page)) await row1.tap();
	else await page.keyboard.press(' ');
	const detail = page.getByTestId('assessments');
	await expect(detail).toContainText(f.assessor);
	await expect(detail).toContainText('score=0.35');
	// Notes go through htmlToText like feed text: markup is read as text, never run.
	await expect(detail).toContainText('note=bold critical');
	await expect(detail.locator('b, img')).toHaveCount(0);
	expect(await page.evaluate(() => (window as unknown as { __pwned?: number }).__pwned)).toBeUndefined();

	// Removing the assessment takes the chip away.
	const del = await page.request.delete(`/api/items/${f.items[0]}/assessments?assessor=${f.assessorId}`, { headers, data: {} });
	expect(del.status()).toBe(204);
	await expect(row1.getByTestId('score-chip')).toHaveCount(0);
});

test('assessments: score: filters and sort:score orders, unassessed: lists the open work', async ({ page }) => {
	const f = fx!;
	await put(page, f.items[0]!, { assessor: f.assessorId, score: 0.2 });
	await put(page, f.items[1]!, { assessor: f.assessorId, score: 0.9 });
	await open(page, `/?source=${f.sourceId}`);
	await expect(rows(page)).toHaveCount(3);
	// Chronological: the score alone does not sort.
	await query(page, `src:#${f.sourceId} score:${f.assessor}`);
	await expect(page).toHaveURL(new RegExp(`assessor=${f.assessorId}`));
	expect(await titles(page)).toEqual([f.title(1), f.title(2), f.title(3)]);
	await expect(page.getByTestId('chip-score')).toContainText(f.assessor);

	// sort:score: highest first, unscored last.
	await query(page, `src:#${f.sourceId} score:${f.assessor} sort:score`);
	await expect(page).toHaveURL(/sort=score/);
	await expect.poll(() => titles(page)).toEqual([f.title(2), f.title(1), f.title(3)]);

	// A minimum.
	await query(page, `src:#${f.sourceId} score:${f.assessor}>=0.5 sort:score`);
	await expect(page).toHaveURL(/min_score=0.5/);
	await expect.poll(() => titles(page)).toEqual([f.title(2)]);

	// Not assessed yet.
	await query(page, `src:#${f.sourceId} unassessed:${f.assessor}`);
	await expect(page).toHaveURL(new RegExp(`unassessed=${f.assessorId}`));
	await expect.poll(() => titles(page)).toEqual([f.title(3)]);

	// The score sort needs an assessor, and the bar says so.
	await query(page, `src:#${f.sourceId} sort:score`);
	await expect(page.getByTestId('query-error')).toHaveText('sort:score needs a score:<assessor> term');
	await page.keyboard.press('Escape');

	// An item that starts matching appears live, in score order.
	await query(page, `src:#${f.sourceId} score:${f.assessor}>=0.5 sort:score`);
	await expect.poll(() => titles(page)).toEqual([f.title(2)]);
	await put(page, f.items[2]!, { assessor: f.assessorId, score: 0.95 });
	await expect.poll(() => titles(page)).toEqual([f.title(3), f.title(2)]);
	// One that stops matching stays until the next reload.
	await put(page, f.items[1]!, { assessor: f.assessorId, score: 0.1 });
	await expect(page.getByTestId('score-chip').filter({ hasText: `${f.assessor} 0.1` })).toHaveCount(1);
	expect(await titles(page)).toEqual([f.title(3), f.title(2)]);
	await page.reload();
	await expect(page.getByTestId('status').locator('[data-status]')).toHaveAttribute('data-status', 'connected');
	await expect.poll(() => titles(page)).toEqual([f.title(3)]);
});

test('assessments: a saved view keeps its score sort, and survives deleting the assessor', async ({ page }) => {
	const f = fx!;
	const name = `Important ${project()}`;
	await put(page, f.items[0]!, { assessor: f.assessorId, score: 0.2 });
	await put(page, f.items[1]!, { assessor: f.assessorId, score: 0.9 });
	await put(page, f.items[2]!, { assessor: f.assessorId, score: 0.5 });
	await open(page, `/?source=${f.sourceId}`);

	await query(page, `src:#${f.sourceId} score:${f.assessor} sort:score`);
	await expect.poll(() => titles(page)).toEqual([f.title(2), f.title(3), f.title(1)]);
	await saveView(page, name);
	await expect(tab(page, name)).toHaveAttribute('aria-current', 'page');
	await expect(page.getByTestId('view-modified')).toHaveCount(0);
	const views = ((await (await page.request.get('/api/views')).json()) as { views: { id: string; name: string; filter: Record<string, unknown> }[] }).views;
	const created = views.find((v) => v.name === name)!;
	expect(created.filter).toMatchObject({ sort: 'score', assessor: f.assessorId });

	// 0 is the unfiltered feed (chronological, every source); 1 is the view,
	// and its order is by score again.
	if (isMobile(page)) await page.getByTestId('view-tab-all').tap();
	else await page.keyboard.press('0');
	await expect(page).not.toHaveURL(/sort=score/);
	await openTab(page, name, '1');
	await expect(page).toHaveURL(/sort=score/);
	await expect(tab(page, name)).toHaveAttribute('aria-current', 'page');
	await expect.poll(() => titles(page)).toEqual([f.title(2), f.title(3), f.title(1)]);

	// Changing the sort marks the tab modified.
	await query(page, `src:#${f.sourceId} score:${f.assessor}`);
	await expect(page.getByTestId('view-modified')).toBeVisible();
	await openTab(page, name, '1');
	await expect(page.getByTestId('view-modified')).toHaveCount(0);

	// Delete the assessor on its page: the confirmation warns about the view.
	if (isMobile(page)) {
		await page.getByTestId('open-filters').tap();
		await page.getByTestId('manage-assessors').tap();
	} else {
		await page.keyboard.press(':');
		await page.keyboard.type('assessors');
		await page.keyboard.press('Enter');
	}
	await expect(page).toHaveURL(/\/assessors$/);
	const arow = page.getByTestId('assessors-list').locator('[role=row]').filter({ hasText: f.assessor });
	await expect(arow).toBeVisible();
	await expect(arow.getByTestId('assessor-scale')).toHaveText('test scale, 0-1');
	if (isMobile(page)) await arow.tap();
	else await arow.click();
	await expect(arow).toHaveAttribute('aria-selected', 'true');
	if (isMobile(page)) await page.getByTestId('tool-delete').tap();
	else await page.keyboard.press('x');
	await expect(page.getByTestId('confirm')).toContainText('1 view filters on this assessor; it will stop filtering on it.');
	if (isMobile(page)) await page.getByTestId('confirm-delete').tap();
	else await page.keyboard.press('y');
	await expect(arow).toHaveCount(0);

	// The view is still there, without the score fields; the feed is chronological.
	const after = ((await (await page.request.get('/api/views')).json()) as { views: { name: string; filter: Record<string, unknown> }[] }).views;
	const kept = after.find((v) => v.name === name);
	expect(kept).toBeDefined();
	expect(kept!.filter.assessor).toBeUndefined();
	expect(kept!.filter.min_score).toBeUndefined();
	expect(kept!.filter.sort === undefined || kept!.filter.sort === 'newest').toBe(true);
	fx = { ...f, assessorId: '0' };
	if (isMobile(page)) await page.getByTestId('nav-feed').tap();
	else await page.keyboard.press('q');
	await expect(page.getByTestId('feed')).toBeVisible();
	await expect(tab(page, name)).toBeVisible();
	await openTab(page, name, '1');
	await expect.poll(() => titles(page)).toEqual(expect.arrayContaining([f.title(1), f.title(2), f.title(3)]));
	await expect(page.getByTestId('score-chip')).toHaveCount(0);
});

test('assessments: assessor names and descriptions are text', async ({ page }) => {
	const evil = `<b>x</b><img src=x onerror="window.__pwned=2"> ${project()}`;
	const res = await page.request.post('/api/assessors', { headers, data: { name: evil, description: '<i>scale</i>' } });
	expect(res.status()).toBe(201);
	const id = ((await res.json()) as { id: string }).id;
	try {
		await page.goto('/assessors');
		const row = page.getByTestId('assessors-list').locator('[role=row]').filter({ hasText: '<b>x</b>' });
		await expect(row).toBeVisible();
		await expect(row.locator('b, i, img')).toHaveCount(0);
		await expect(row.getByTestId('assessor-scale')).toHaveText('<i>scale</i>');
		expect(await page.evaluate(() => (window as unknown as { __pwned?: number }).__pwned)).toBeUndefined();
	} finally {
		await page.request.delete(`/api/assessors/${id}`, { headers, data: {} });
	}
});

test('assessments: the assessors page adds, edits and removes', async ({ page }) => {
	const name = `eval-${project()}`;
	await page.goto('/assessors');
	await expect(page.getByTestId('assessors-list')).toBeVisible();
	const tool = async (t: 'add' | 'edit') => {
		if (isMobile(page)) await page.getByTestId('tool-' + t).tap();
		else await page.keyboard.press(t === 'add' ? 'a' : 'e');
	};
	const submit = async (field: Locator) => {
		if (isMobile(page)) await page.getByTestId('save').tap();
		else {
			await field.focus();
			await page.keyboard.press('Enter');
		}
	};
	try {
		await tool('add');
		await expect(page.getByTestId('assessor-name')).toBeFocused();
		await page.getByTestId('assessor-name').fill(name);
		await page.getByTestId('assessor-description').fill('first scale');
		await submit(page.getByTestId('assessor-name'));
		const row = page.getByTestId('assessors-list').locator('[role=row]').filter({ hasText: name });
		await expect(row).toBeVisible();
		await expect(row).toHaveAttribute('aria-selected', 'true');
		await expect(row.getByTestId('assessor-scale')).toHaveText('first scale');

		// A duplicate name is refused with the daemon's message.
		await tool('add');
		await page.getByTestId('assessor-name').fill(name);
		await submit(page.getByTestId('assessor-name'));
		await expect(page.getByTestId('assessor-form')).toContainText('already exists');
		if (isMobile(page)) await page.getByTestId('assessor-form').getByRole('button', { name: 'cancel' }).tap();
		else await page.keyboard.press('Escape');

		await tool('edit');
		await page.getByTestId('assessor-description').fill('second scale');
		await submit(page.getByTestId('assessor-name'));
		await expect(row.getByTestId('assessor-scale')).toHaveText('second scale');
	} finally {
		const list = ((await (await page.request.get('/api/assessors')).json()) as { assessors?: { id: string; name: string }[] }).assessors ?? [];
		for (const a of list) if (a.name === name) await page.request.delete(`/api/assessors/${a.id}`, { headers, data: {} });
	}
});
