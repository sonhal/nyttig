import { expect, test, type Page } from '@playwright/test';
import { BASE_URL, FEEDS_URL } from './feeds.mjs';

// Digests against the real daemon, at the desktop viewport (keys) and the
// mobile one (taps): an assessor, a series and two digests are created
// through the API (the second has the first as input and two items linked),
// then read, followed through the input link and the history, edited and
// deleted. Both projects share one daemon, so every name carries the project
// and everything created is removed again, even when a test fails.

const isMobile = (page: Page) => (page.viewportSize()?.width ?? 1000) < 720;
const project = () => test.info().project.name;
const headers = { Origin: BASE_URL };

interface Fixture {
	sourceId: string;
	assessorId: string;
	assessor: string;
	seriesId: string;
	series: string;
	older: string;
	newer: string;
	olderTitle: string;
	newerTitle: string;
	itemTitles: string[];
	itemIds: string[];
}

let fx: Fixture | undefined;

const HOSTILE = '<img src=x onerror="window.__pwned=1"> and [click](javascript:window.__pwned=2) <script>window.__pwned=3</script>';

async function post<T>(page: Page, path: string, data: unknown, status = 201): Promise<T> {
	const res = await page.request.post(path, { headers, data });
	expect(res.status(), await res.text()).toBe(status);
	return (await res.json()) as T;
}

async function setup(page: Page): Promise<Fixture> {
	const slug = `dgx-${project()}`;
	const src = await post<{ id: string }>(page, '/api/sources', {
		name: `Bulk ${slug}`,
		url: `${FEEDS_URL}/bulk/${slug}.xml?n=3`,
		refresh_sec: 3600
	});
	await expect
		.poll(async () => ((await (await page.request.get(`/api/items?source=${src.id}&limit=1`)).json()) as { total?: number }).total, {
			timeout: 20_000
		})
		.toBe(3);
	const list = (await (await page.request.get(`/api/items?source=${src.id}&limit=10`)).json()) as { items: { id: string; title: string }[] };

	const assessor = `dg-${project()}`;
	const a = await post<{ id: string }>(page, '/api/assessors', { name: assessor, description: 'digest test', color: '#D97757' });
	const series = `daily-${project()}`;
	const s = await post<{ id: string }>(page, '/api/digest-series', { assessor: a.id, name: series, description: 'daily test digests' });

	const olderTitle = `Older digest ${project()}`;
	const newerTitle = `Newer digest ${project()}`;
	const older = await post<{ id: string }>(page, '/api/digests', {
		series: s.id,
		title: olderTitle,
		body: `# Day one\n\n${HOSTILE}\n\n- first point\n- second point`,
		// Whole days in the browser's time zone (Europe/Oslo, see playwright.config.ts).
		period_start: '2026-10-04T00:00:00+02:00',
		period_end: '2026-10-04T23:59:59+02:00'
	});
	const newer = await post<{ id: string }>(page, '/api/digests', {
		series: s.id,
		title: newerTitle,
		body: `Day two builds on **day one**.\n\nSee [#${list.items[0]?.id}] and [#999999].`,
		period_start: '2026-10-05T00:00:00+02:00',
		period_end: '2026-10-05T23:59:59+02:00',
		items: list.items.slice(0, 2).map((i) => i.id),
		inputs: [older.id]
	});
	return {
		sourceId: src.id,
		assessorId: a.id,
		assessor,
		seriesId: s.id,
		series,
		older: older.id,
		newer: newer.id,
		olderTitle,
		newerTitle,
		itemTitles: list.items.slice(0, 2).map((i) => i.title),
		itemIds: list.items.slice(0, 2).map((i) => i.id)
	};
}

async function cleanup(page: Page) {
	if (!fx) return;
	// Deleting the assessor takes its series and digests (the series test may
	// have deleted them already).
	await page.request.delete(`/api/assessors/${fx.assessorId}`, { headers, data: {} });
	await page.request.delete(`/api/sources/${fx.sourceId}`, { headers, data: {} });
	fx = undefined;
}

test.afterEach(async ({ page }) => {
	await cleanup(page);
});

const title = (page: Page) => page.getByTestId('digest-title');

async function open(page: Page, path: string) {
	await page.goto(path);
	// On a phone a series in the URL shows its history, so the list may be hidden.
	await expect(page.getByTestId('series-row').first()).toBeAttached();
}

test('reads a digest with its items and inputs, and follows the input and the history', async ({ page }) => {
	fx = await setup(page);
	const f = fx;
	const pwned = () => page.evaluate(() => (window as unknown as { __pwned?: number }).__pwned);

	await open(page, `/digests?series=${f.seriesId}`);

	if (isMobile(page)) {
		// A series in the URL opens its history; back is the list of series.
		await expect(page.getByTestId('history-row')).toHaveCount(2);
		await page.getByTestId('back').tap();
	}
	// The series list names the assessor, the series and how many digests it has.
	const row = page.getByTestId('series-row').filter({ hasText: f.series });
	await expect(row).toBeVisible();
	await expect(row).toContainText('2');
	await expect(page.getByTestId('series-group').filter({ hasText: f.assessor })).toBeVisible();
	if (isMobile(page)) {
		await row.tap();
		await page.getByTestId('history-row').filter({ hasText: f.newerTitle }).tap();
	}

	// No digest in the URL: the newest one is shown.
	await expect(title(page)).toHaveText(f.newerTitle);
	await expect(title(page)).toBeVisible();
	await expect(page.getByTestId('digest-series')).toHaveText(f.series);
	await expect(page.getByTestId('digest-period')).toHaveText('2026-10-05');
	await expect(page.getByTestId('digest-body')).toContainText('Day two builds on day one.');
	await expect(page.getByTestId('digest-body').locator('strong')).toHaveText('day one');
	// [#id] links to a linked item (title as the tooltip); an unknown id stays text.
	const ref = page.getByTestId('item-ref');
	await expect(ref).toHaveCount(1);
	await expect(ref).toHaveText(`[#${f.itemIds[0]}]`);
	await expect(ref).toHaveAttribute('title', f.itemTitles[0] ?? '');
	await expect(ref).toHaveAttribute('href', /^https?:\/\//);
	await expect(page.getByTestId('digest-body')).toContainText('[#999999]');

	// The items it is based on: links to the articles, titles as text.
	await expect(page.getByTestId('digest-items-title')).toHaveText('Based on 2 items');
	const items = page.getByTestId('digest-item');
	await expect(items).toHaveCount(2);
	for (const t of f.itemTitles) {
		const link = items.locator('a', { hasText: t });
		await expect(link).toHaveAttribute('href', /^https?:\/\//);
		await expect(link).toHaveAttribute('rel', /noopener/);
	}

	// The input opens the older digest, and the URL learns its series.
	const input = page.getByTestId('digest-input');
	await expect(input).toHaveCount(1);
	await expect(input).toContainText(f.olderTitle);
	if (isMobile(page)) await input.tap();
	else await input.click();
	await expect(title(page)).toHaveText(f.olderTitle);
	await expect(page).toHaveURL(new RegExp(`series=${f.seriesId}&digest=${f.older}$`));
	await expect(page.getByTestId('digest-period')).toHaveText('2026-10-04');

	// Hostile text in the body is text: nothing is built from it, nothing ran.
	const body = page.getByTestId('digest-body');
	await expect(body).toContainText('<img src=x onerror="window.__pwned=1">');
	// A link with an unsafe target shows its text and is no link.
	await expect(body).toContainText('> and click <script>');
	await expect(body).toContainText('<script>window.__pwned=3</script>');
	await expect(body.locator('img, script, a')).toHaveCount(0);
	// The rest of the Markdown is rendered.
	await expect(body.locator('h3')).toHaveText('Day one');
	await expect(body.locator('li')).toHaveText(['first point', 'second point']);
	expect(await pwned()).toBeUndefined();
	await expect(page.getByTestId('digest-inputs-title')).toHaveCount(0);

	// The history: back to the newer one through it.
	if (isMobile(page)) await page.getByTestId('back').tap();
	const hist = page.getByTestId('history-row');
	await expect(hist).toHaveCount(2);
	await expect(hist.first()).toContainText(f.newerTitle);
	await expect(hist.nth(1)).toContainText(f.olderTitle);
	if (isMobile(page)) await hist.first().tap();
	else await hist.first().click();
	await expect(title(page)).toHaveText(f.newerTitle);
	await expect(page).toHaveURL(new RegExp(`digest=${f.newer}$`));

	if (!isMobile(page)) {
		// Keys: j goes to the older digest, k back, 1 opens the first input.
		await page.keyboard.press('j');
		await expect(title(page)).toHaveText(f.olderTitle);
		await page.keyboard.press('k');
		await expect(title(page)).toHaveText(f.newerTitle);
		await page.keyboard.press('1');
		await expect(title(page)).toHaveText(f.olderTitle);
		await page.keyboard.press('g');
		await expect(title(page)).toHaveText(f.newerTitle);
		await page.keyboard.press('G');
		await expect(title(page)).toHaveText(f.olderTitle);
	}
	expect(await pwned()).toBeUndefined();
});

test('opens a series from the command line or the sheet, and a missing digest says so', async ({ page }) => {
	fx = await setup(page);
	const f = fx;

	await page.goto('/');
	await expect(page.getByTestId('status').locator('[data-status]')).toHaveAttribute('data-status', 'connected');
	if (isMobile(page)) {
		// The feed's digests button opens the page, and the pinned feed tab goes back.
		await page.getByTestId('open-digests').tap();
		await expect(page).toHaveURL(/\/digests$/);
		await expect(page.getByTestId('nav-feed')).toBeInViewport();
		await page.getByTestId('nav-feed').tap();
		await expect(page).toHaveURL(/\/$/);
		await page.getByTestId('open-filters').tap();
		await page.getByTestId('manage-digests').tap();
		await expect(page).toHaveURL(/\/digests$/);
		await page.getByTestId('series-row').filter({ hasText: f.series }).tap();
	} else {
		await page.keyboard.press(':');
		await page.keyboard.type(`digest ${f.assessor}/${f.series}`);
		await page.keyboard.press('Enter');
		await expect(page).toHaveURL(new RegExp(`/digests\\?series=${f.seriesId}$`));
		await expect(title(page)).toHaveText(f.newerTitle);
		// ":q" returns to the feed, and ":digests" comes back.
		await page.keyboard.press('q');
		await expect(page).toHaveURL(/\/$/);
		await page.keyboard.press(':');
		await page.keyboard.type('digests');
		await page.keyboard.press('Enter');
		await expect(page).toHaveURL(/\/digests$/);
	}
	await expect(page.getByTestId('nav-digests')).toHaveAttribute('aria-current', 'page');

	await page.goto('/digests?series=' + f.seriesId + '&digest=999999');
	await expect(page.getByRole('alert')).toContainText('does not exist');
});

test('edits, reorders and deletes a series, saying how many digests go', async ({ page }) => {
	fx = await setup(page);
	const f = fx;
	await open(page, `/digests?series=${f.seriesId}`);
	if (isMobile(page)) await expect(page.getByTestId('history-row')).toHaveCount(2);

	// Rename and redescribe.
	const renamed = `renamed-${project()}`;
	if (isMobile(page)) await page.getByTestId('tool-edit').tap();
	else await page.keyboard.press('e');
	await expect(page.getByTestId('series-form')).toBeVisible();
	await page.getByTestId('series-name').fill(renamed);
	await page.getByTestId('series-description').fill('new description');
	if (isMobile(page)) await page.getByTestId('save').tap();
	else await page.keyboard.press('Enter');
	await expect(page.getByTestId('series-row').filter({ hasText: renamed })).toBeAttached();
	await expect(page.getByTestId('series-form')).toHaveCount(0);
	const got = (await (await page.request.get(`/api/digest-series?assessor=${f.assessorId}`)).json()) as {
		series: { name: string; description: string }[];
	};
	expect(got.series[0]).toMatchObject({ name: renamed, description: 'new description' });

	// A second series of the assessor, to move this one below it.
	const other = await post<{ id: string }>(page, '/api/digest-series', { assessor: f.assessorId, name: `other-${project()}` });
	await page.reload();
	await expect(page.getByTestId('series-row').filter({ hasText: `other-${project()}` })).toBeAttached();
	const names = () => page.getByTestId('series-row').filter({ hasText: project() }).allTextContents();
	expect((await names())[0]).toContain(renamed);
	if (isMobile(page)) await page.getByTestId('tool-moveDown').tap();
	else await page.keyboard.press('J');
	await expect.poll(async () => (await names())[0]).toContain(`other-${project()}`);
	const order = (await (await page.request.get(`/api/digest-series?assessor=${f.assessorId}`)).json()) as { series: { id: string }[] };
	expect(order.series.map((s) => s.id)).toEqual([other.id, f.seriesId]);
	await page.request.delete(`/api/digest-series/${other.id}`, { headers, data: {} });

	// Delete: the confirmation says how many digests go.
	if (isMobile(page)) await page.getByTestId('tool-delete').tap();
	else await page.keyboard.press('x');
	await expect(page.getByTestId('confirm')).toContainText('2 digests will be deleted');
	if (isMobile(page)) await page.getByTestId('confirm-delete').tap();
	else await page.keyboard.press('y');
	await expect(page.getByTestId('series-row').filter({ hasText: renamed })).toHaveCount(0);
	expect((await page.request.get(`/api/digests/${f.newer}`)).status()).toBe(404);
	expect((await page.request.get(`/api/digests/${f.older}`)).status()).toBe(404);
});

test('the assessor\'s delete confirmation says how many series and digests go with it', async ({ page }) => {
	fx = await setup(page);
	const f = fx;
	await page.goto('/assessors');
	const row = page.getByTestId('assessors-list').locator('[role=row]').filter({ hasText: f.assessor });
	if (isMobile(page)) await row.tap();
	else await row.click();
	if (isMobile(page)) await page.getByTestId('tool-delete').tap();
	else await page.keyboard.press('x');
	await expect(page.getByTestId('confirm')).toContainText('1 digest series; 2 digests will be deleted with it');
	if (isMobile(page)) await page.getByRole('button', { name: 'cancel' }).tap();
	else await page.keyboard.press('n');
	await expect(page.getByTestId('confirm')).toHaveCount(0);
});

test('the pages of a long history load on demand', async ({ page }) => {
	fx = await setup(page);
	const f = fx;
	// 22 more digests: with the two there are 24, the page is 20.
	for (let i = 0; i < 22; i++) {
		const day = String(i + 1).padStart(2, '0');
		await post(page, '/api/digests', {
			series: f.seriesId,
			title: `Old ${day} ${project()}`,
			body: 'b',
			period_start: `2026-09-${day}T00:00:00Z`,
			period_end: `2026-09-${day}T23:59:59Z`
		});
	}
	await open(page, `/digests?series=${f.seriesId}`);
	const hist = page.getByTestId('history-row');
	await expect(hist).toHaveCount(20);
	const more = page.getByTestId('load-older');
	await expect(more).toBeVisible();
	if (isMobile(page)) await more.tap();
	else await more.click();
	await expect(hist).toHaveCount(24);
	await expect(more).toHaveCount(0);
	await expect(hist.last()).toContainText('Old 01');

	// j at the end of what is loaded is not needed here, but G reaches the oldest loaded.
	if (!isMobile(page)) {
		await page.keyboard.press('G');
		await expect(title(page)).toHaveText(`Old 01 ${project()}`);
	}
});
