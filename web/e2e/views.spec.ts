import { expect, test, type Locator, type Page } from '@playwright/test';
import { BASE_URL } from './feeds.mjs';

// Saved views against the real daemon, at the desktop viewport (keys) and
// the mobile one (taps): saving a filter as a tab, switching, the "*" of a
// modified view, the picker, the management page (favorite, reorder,
// delete) and what deleting the view's tag does. Both projects share one
// daemon, so every name carries the project and everything created is
// removed again, even when a test fails.

const isMobile = (page: Page) => (page.viewportSize()?.width ?? 1000) < 720;
const project = () => test.info().project.name;
const headers = { Origin: BASE_URL };

interface ViewJSON {
	id: string;
	name: string;
	filter: { q?: string; source?: string; tag?: string; sort?: string; unviewed?: boolean };
	favorite?: boolean;
	position?: number;
}

const apiViews = async (page: Page): Promise<ViewJSON[]> =>
	((await (await page.request.get('/api/views')).json()) as { views: ViewJSON[] }).views;

/** A view's filter by id or name, without the default sort the daemon spells out. */
function filterOf(views: ViewJSON[], idOrName: string): ViewJSON['filter'] | undefined {
	const f = views.find((v) => v.id === idOrName || v.name === idOrName)?.filter;
	if (!f) return undefined;
	const { sort, ...rest } = f;
	return sort && sort !== 'newest' ? { ...rest, sort } : rest;
}

async function addTag(page: Page, name: string): Promise<string> {
	const res = await page.request.post('/api/tags', { headers, data: { name } });
	expect(res.status()).toBe(201);
	return ((await res.json()) as { id: string }).id;
}

const search = (page: Page) => page.getByTestId('search');
const tab = (page: Page, name: string) => page.getByTestId('view-tab').filter({ hasText: name });

let tagId = '';
const tagName = () => `vtag-${project()}`;

test.beforeEach(async ({ page }) => {
	await page.goto('/');
	tagId = await addTag(page, tagName());
});

test.afterEach(async ({ page }) => {
	// Whatever the test left: this project's views and tag.
	for (const v of await apiViews(page)) {
		if (v.name.endsWith(` ${project()}`)) await page.request.delete(`/api/views/${v.id}`, { headers, data: {} });
	}
	await page.request.delete(`/api/tags/${tagId}`, { headers, data: {} });
});

async function openFeed(page: Page, path = '/') {
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

/** Runs ":<text>" (the "+ save" tab starts the line with "save " on mobile, where there is no ":"). */
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

type Tool = 'add' | 'edit' | 'delete' | 'toggle' | 'moveUp' | 'moveDown';
const KEYS: Record<Tool, string> = { add: 'a', edit: 'e', delete: 'x', toggle: ' ', moveUp: 'K', moveDown: 'J' };

async function act(page: Page, tool: Tool) {
	if (isMobile(page)) await page.getByTestId('tool-' + tool).tap();
	else await page.keyboard.press(KEYS[tool]);
}

async function pick(row: Locator) {
	if (isMobile(row.page())) await row.tap();
	else await row.click();
	await expect(row).toHaveAttribute('aria-selected', 'true');
}

async function save(page: Page, field: Locator) {
	if (isMobile(page)) await page.getByTestId('save').tap();
	else {
		await field.focus();
		await page.keyboard.press('Enter');
	}
}

async function confirmDelete(page: Page) {
	if (isMobile(page)) await page.getByTestId('confirm-delete').tap();
	else await page.keyboard.press('y');
}

const rowsOf = (page: Page, view: string) => page.getByTestId(view + '-list').locator('[role=row]');
const rowOf = (page: Page, view: string, text: string) => rowsOf(page, view).filter({ hasText: text });

/** Goes from the feed to a management page the way a user would. */
async function goTo(page: Page, view: 'tags' | 'views') {
	if (isMobile(page)) {
		await page.getByTestId('open-filters').tap();
		await page.getByTestId('manage-' + view).tap();
	} else {
		await page.keyboard.press(':');
		await page.keyboard.type(view);
		await page.keyboard.press('Enter');
	}
	await expect(page).toHaveURL(new RegExp(`/${view}$`));
	await expect(page.getByTestId(view + '-list')).toBeVisible();
}

async function backToFeed(page: Page) {
	if (isMobile(page)) await page.getByTestId('nav-feed').tap();
	else await page.keyboard.press('q');
	await expect(page.getByTestId('feed')).toBeVisible();
}

/** Opens a tab: a tap, or its number key on desktop. */
async function openTab(page: Page, name: string, key: string) {
	if (isMobile(page)) await tab(page, name).tap();
	else await page.keyboard.press(key);
}

test('views: save a filter as a tab, switch, modify, update, pick', async ({ page }) => {
	const name = `Sec ${project()}`;
	const q = `tag:${tagName()} sort:oldest`;
	await openFeed(page);
	await expect(page.getByTestId('view-tab-all')).toHaveAttribute('aria-current', 'page');

	// :save <name> makes a favorite tab from the filter and opens it.
	await query(page, q);
	await expect(page).toHaveURL(new RegExp(`tag=${tagId}`));
	await command(page, `save ${name}`);
	await expect(tab(page, name)).toBeVisible();
	await expect(tab(page, name)).toHaveAttribute('aria-current', 'page');
	await expect(page.getByTestId('view-tab-all')).not.toHaveAttribute('aria-current', 'page');
	await expect(page).toHaveURL(/view=\d+/);
	await expect(page).toHaveURL(new RegExp(`tag=${tagId}`));
	await expect(page.getByTestId('view-modified')).toHaveCount(0);
	const [created] = (await apiViews(page)).filter((v) => v.name === name);
	expect(created).toMatchObject({ favorite: true, filter: { tag: tagId, sort: 'oldest' } });
	if (!isMobile(page)) await expect(tab(page, name)).toContainText('1');

	// 0 (or "all") is the unfiltered feed; 1 (or the tab) restores the view.
	if (isMobile(page)) await page.getByTestId('view-tab-all').tap();
	else await page.keyboard.press('0');
	await expect(page).not.toHaveURL(/view=/);
	await expect(page).not.toHaveURL(/tag=/);
	await expect(search(page)).toHaveValue('');
	await expect(page.getByTestId('view-tab-all')).toHaveAttribute('aria-current', 'page');
	await openTab(page, name, '1');
	await expect(page).toHaveURL(new RegExp(`view=${created!.id}`));
	await expect(search(page)).toHaveValue(q);
	await expect(tab(page, name)).toHaveAttribute('aria-current', 'page');

	// Changing the filter keeps the tab open and marks it modified.
	await query(page, `${q} is:unviewed`);
	await expect(page).toHaveURL(new RegExp(`view=${created!.id}`));
	await expect(page).toHaveURL(/unviewed=1/);
	await expect(page.getByTestId('view-modified')).toBeVisible();
	await expect(tab(page, name)).toContainText('*');
	// A command that is not a filter command keeps both, and the tab link resets the filter.
	await openTab(page, name, '1');
	await expect(page.getByTestId('view-modified')).toHaveCount(0);
	await expect(search(page)).toHaveValue(q);

	// :save without a name writes the filter into the open view.
	await query(page, `${q} is:unviewed`);
	await expect(page.getByTestId('view-modified')).toBeVisible();
	await command(page, 'save');
	await expect(page.getByTestId('view-modified')).toHaveCount(0);
	await expect.poll(async () => filterOf(await apiViews(page), created!.id)).toEqual({
		tag: tagId,
		sort: 'oldest',
		unviewed: true
	});
	// A reload comes back to the same tab, unmodified.
	await page.reload();
	await expect(page.getByTestId('view-tabs')).toBeVisible();
	await expect(tab(page, name)).toHaveAttribute('aria-current', 'page');
	await expect(page.getByTestId('view-modified')).toHaveCount(0);
	await expect(search(page)).toHaveValue(`tag:${tagName()} is:unviewed sort:oldest`);

	if (!isMobile(page)) {
		// Keys without a favorite say so.
		await page.keyboard.press('0');
		await page.keyboard.press('9');
		await expect(page.getByText('no favorite view 9')).toBeVisible();
		// The picker lists every view, favorites starred; Enter opens it.
		await page.keyboard.press('v');
		await expect(page.getByTestId('picker')).toBeVisible();
		await expect(page.getByTestId('picker').getByRole('option', { name: `★ ${name}` })).toBeVisible();
		await page.getByTestId('picker-input').fill('sec');
		await page.keyboard.press('Enter');
		await expect(page.getByTestId('picker')).toHaveCount(0);
		await expect(page).toHaveURL(new RegExp(`view=${created!.id}`));
		// :view <name> and :view all.
		await command(page, 'view all');
		await expect(page).not.toHaveURL(/view=/);
		await command(page, `view ${name}`);
		await expect(page).toHaveURL(new RegExp(`view=${created!.id}`));
		// The help lists the keys.
		await page.keyboard.press('?');
		const help = page.getByRole('dialog').first();
		await expect(help).toContainText('go to favorite view 1-9');
		await expect(help).toContainText('pick a saved view by name');
		await page.keyboard.press('Escape');
	}
});

test('views: the management page (add, favorite, reorder, delete), and deleting the view\'s tag', async ({ page }) => {
	const first = `Sec ${project()}`;
	const second = `Second ${project()}`;
	await openFeed(page);
	await query(page, `tag:${tagName()} is:unviewed`);
	await command(page, `save ${first}`);
	await expect(tab(page, first)).toBeVisible();

	await goTo(page, 'views');
	const firstRow = rowOf(page, 'views', first);
	await expect(firstRow).toContainText('★');
	await expect(firstRow.getByTestId('view-filter')).toHaveText(`tag:${tagName()} is:unviewed`);

	// Add: the query is checked with the "/" bar's parser.
	await act(page, 'add');
	await expect(page.getByTestId('view-name')).toBeFocused();
	await page.getByTestId('view-name').fill(second);
	await page.getByTestId('view-query').fill('tag:nope');
	await save(page, page.getByTestId('view-name'));
	await expect(page.getByTestId('view-query-error')).toHaveText('unknown tag: nope');
	await page.getByTestId('view-query').fill('sort:oldest');
	await save(page, page.getByTestId('view-name'));
	await expect(page.getByTestId('view-form')).toHaveCount(0);
	const secondRow = rowOf(page, 'views', second);
	await expect(secondRow).toBeVisible();
	await expect(secondRow).toHaveAttribute('aria-selected', 'true');
	await expect(secondRow).toContainText('★');
	await expect(secondRow.getByTestId('view-filter')).toHaveText('sort:oldest');

	// A second view with the same name (any case) is refused.
	await act(page, 'add');
	await page.getByTestId('view-name').fill(second.toUpperCase());
	await save(page, page.getByTestId('view-name'));
	await expect(page.getByTestId('view-form')).toContainText('already exists');
	if (isMobile(page)) await page.getByTestId('view-form').getByRole('button', { name: 'cancel' }).tap();
	else await page.keyboard.press('Escape');
	await expect(page.getByTestId('view-form')).toHaveCount(0);

	// Favorite toggle.
	await pick(secondRow);
	await act(page, 'toggle');
	await expect(secondRow).toContainText('☆');
	await act(page, 'toggle');
	await expect(secondRow).toContainText('★');

	// Reorder: the second view moves above the first, and the tabs follow.
	const order = async () => (await rowsOf(page, 'views').allTextContents()).map((t) => (t.includes(second) ? 'second' : 'first'));
	expect(await order()).toEqual(['first', 'second']);
	await act(page, 'moveUp');
	await expect.poll(order).toEqual(['second', 'first']);
	await expect(secondRow).toHaveAttribute('aria-selected', 'true');
	await expect.poll(async () => (await apiViews(page)).map((v) => v.name)).toEqual([second, first]);
	await backToFeed(page);
	await expect(page.getByTestId('view-tab')).toHaveText([new RegExp(second), new RegExp(first)]);
	await goTo(page, 'views');
	await pick(rowOf(page, 'views', second));
	await act(page, 'moveDown');
	await expect.poll(order).toEqual(['first', 'second']);

	// Edit the filter and name.
	await act(page, 'edit');
	await page.getByTestId('view-query').fill('');
	await save(page, page.getByTestId('view-name'));
	await expect(secondRow.getByTestId('view-filter')).toHaveText('no filter');

	// Delete it.
	await act(page, 'delete');
	await expect(page.getByTestId('confirm')).toContainText(second);
	await confirmDelete(page);
	await expect(secondRow).toHaveCount(0);
	await expect(firstRow).toBeVisible();

	// Deleting the tag the first view filters on warns, and keeps the view.
	await backToFeed(page);
	await goTo(page, 'tags');
	await pick(rowOf(page, 'tags', tagName()));
	await act(page, 'delete');
	await expect(page.getByTestId('confirm')).toContainText('1 view filters on this tag; it will stop filtering on it.');
	await confirmDelete(page);
	await expect(rowOf(page, 'tags', tagName())).toHaveCount(0);
	await backToFeed(page);
	await goTo(page, 'views');
	await expect(firstRow).toBeVisible();
	await expect(firstRow.getByTestId('view-filter')).toHaveText('is:unviewed');
	await expect.poll(async () => filterOf(await apiViews(page), first)).toEqual({ unviewed: true });
	await backToFeed(page);
	await expect(tab(page, first)).toBeVisible();
});

test('views: names are text, and the tabs scroll inside their own row', async ({ page }) => {
	const evil = `<b>bold</b><img src=x onerror="window.__pwned=1"> ${project()}`;
	const res = await page.request.post('/api/views', { headers, data: { name: evil, favorite: true } });
	expect(res.status()).toBe(201);
	for (let i = 1; i <= 8; i++) {
		const r = await page.request.post('/api/views', {
			headers,
			data: { name: `A long tab name number ${i} ${project()}`, favorite: true }
		});
		expect(r.status()).toBe(201);
	}
	await openFeed(page);
	const tabs = page.getByTestId('view-tabs');
	await expect(page.getByTestId('view-tab')).toHaveCount(9);
	// The name is text: no elements from it, and nothing ran.
	await expect(tab(page, '<b>bold</b>')).toBeVisible();
	await expect(tabs.locator('b, img')).toHaveCount(0);
	expect(await page.evaluate(() => (window as unknown as { __pwned?: number }).__pwned)).toBeUndefined();
	// More tabs than fit: the row scrolls, the page does not.
	const m = await tabs.evaluate((el) => ({
		overflow: el.scrollWidth > el.clientWidth,
		scrolls: getComputedStyle(el).overflowX,
		page: document.documentElement.scrollWidth <= document.documentElement.clientWidth
	}));
	expect(m).toEqual({ overflow: true, scrolls: 'auto', page: true });
	if (isMobile(page)) {
		expect((await tab(page, 'number 1 ').boundingBox())?.height).toBe(44);
		await tabs.evaluate((el) => (el.scrollLeft = el.scrollWidth));
		await expect(tab(page, 'number 8 ')).toBeInViewport();
		await tab(page, 'number 8 ').tap();
		await expect(page).toHaveURL(/view=\d+/);
	} else {
		// Only the first nine have a number key; the first tab shows its number.
		await expect(page.getByTestId('view-tab').first()).toContainText('1');
	}
});
