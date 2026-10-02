import { expect, test, type Locator, type Page } from '@playwright/test';
import { BASE_URL, FEEDS_URL } from './feeds.mjs';

// The log-viewer features (follow mode, query syntax, load older, help,
// command line, pickers, highlighting, time toggle) against the real
// daemon, at the desktop and the mobile viewport. Both projects share one
// daemon, so a test that needs a long list adds its own source (a bulk
// feed on the feed server) and removes it again, and filters to it.

const isMobile = (page: Page) => (page.viewportSize()?.width ?? 1000) < 720;
const project = () => test.info().project.name;

const feedEl = (page: Page) => page.getByTestId('feed');
const rows = (page: Page) => page.locator('[role=row]');
const row = (page: Page, title: string) => rows(page).filter({ hasText: title });
/** A row by its exact title ("item 1" must not match "item 10"). */
const titled = (page: Page, title: string) =>
	rows(page).filter({ has: page.locator('.title', { hasText: new RegExp(`^${title.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}$`) }) });
const search = (page: Page) => page.getByTestId('search');
const follow = (page: Page) => page.getByTestId('follow');

async function open(page: Page, path = '/') {
	await page.goto(path);
	await expect(page.getByTestId('status').locator('[data-status]')).toHaveAttribute('data-status', 'connected');
	await expect(feedEl(page)).toBeVisible();
}

/** Opens a management view and waits for its first row, so the names are loaded. */
async function openManage(page: Page, view: 'sources' | 'tags') {
	await page.goto('/' + view);
	await expect(page.getByTestId(view + '-list').locator('[role=row]').first()).toBeVisible();
}

const headers = { Origin: BASE_URL };

interface Bulk {
	id: string;
	slug: string;
	/** Title of the n-th item (1 is the newest). */
	title: (n: number) => string;
}

/** Adds a source with a bulk feed of n items and waits until they are all in. */
async function addBulk(page: Page, label: string, n: number): Promise<Bulk> {
	const slug = `${label}-${project()}`;
	const res = await page.request.post('/api/sources', {
		headers,
		data: { name: `Bulk ${slug}`, url: `${FEEDS_URL}/bulk/${slug}.xml?n=${n}`, refresh_sec: 3600 }
	});
	expect(res.status()).toBe(201);
	const { id } = (await res.json()) as { id: string };
	await expect
		.poll(async () => ((await (await page.request.get(`/api/items?source=${id}&limit=1`)).json()) as { total?: number }).total, {
			timeout: 20_000
		})
		.toBe(n);
	return { id, slug, title: (k) => `${slug} item ${k}` };
}

async function removeSource(page: Page, id: string) {
	const res = await page.request.delete(`/api/sources/${id}`, { headers, data: {} });
	expect(res.status()).toBe(204);
}

/** Puts an item at the top of a bulk feed and fetches the source. */
async function pushBulk(page: Page, b: Bulk, title: string) {
	const add = await page.request.post(`${FEEDS_URL}/add?feed=bulk/${b.slug}&title=${encodeURIComponent(title)}`);
	expect(add.status()).toBe(204);
	const res = await page.request.post(`/api/refresh?source=${b.id}`, { headers, data: {} });
	expect(res.status()).toBe(204);
}

const scrollTop = (feed: Locator) => feed.evaluate((el) => el.scrollTop);

/** Types into the query bar. */
async function query(page: Page, text: string) {
	if (isMobile(page)) await search(page).tap();
	else await page.keyboard.press('/');
	await search(page).fill(text);
}

async function submit(page: Page) {
	await search(page).press('Enter');
}

// ── Follow mode ───────────────────────────────────────────────

test('follow: sticks to the newest at the top, counts "N new" after scrolling away, F jumps back', async ({ page }) => {
	const b = await addBulk(page, 'follow', 60);
	try {
		await open(page, `/?source=${b.id}`);
		await expect(titled(page, b.title(1))).toBeVisible();
		await expect(follow(page)).toHaveAttribute('data-follow', 'on');

		// At the top, a pushed item flows in and the view stays on the newest.
		await pushBulk(page, b, `${b.slug} fresh 1`);
		await expect(rows(page).first()).toContainText(`${b.slug} fresh 1`);
		expect(await scrollTop(feedEl(page))).toBe(0);
		await expect(follow(page)).toHaveAttribute('data-follow', 'on');

		// Scroll away: follow is off, and the view stays put while items arrive.
		await feedEl(page).evaluate((el) => (el.scrollTop = 400));
		await expect(follow(page)).toHaveAttribute('data-follow', 'off');
		const rowH = isMobile(page) ? 44 : 20;
		const before = await scrollTop(feedEl(page));
		const topTitle = await rowAtTop(feedEl(page));

		await pushBulk(page, b, `${b.slug} fresh 2`);
		await expect(follow(page)).toHaveAttribute('data-pending', '1');
		await expect(follow(page)).toContainText('↑ 1 new');
		// The row that was at the top of the view still is: the scroll position
		// moved down by the one row that was inserted above it.
		await expect.poll(() => scrollTop(feedEl(page))).toBe(before + rowH);
		expect(await rowAtTop(feedEl(page))).toBe(topTitle);

		await pushBulk(page, b, `${b.slug} fresh 3`);
		await expect(follow(page)).toHaveAttribute('data-pending', '2');
		await expect(follow(page)).toContainText('↑ 2 new');

		// F (or a tap on the status bar) jumps to the newest and sticks again.
		if (isMobile(page)) await follow(page).tap();
		else await page.keyboard.press('F');
		await expect(follow(page)).toHaveAttribute('data-follow', 'on');
		expect(await scrollTop(feedEl(page))).toBe(0);
		await expect(rows(page).first()).toContainText(`${b.slug} fresh 3`);

		await pushBulk(page, b, `${b.slug} fresh 4`);
		await expect(rows(page).first()).toContainText(`${b.slug} fresh 4`);
		expect(await scrollTop(feedEl(page))).toBe(0);
	} finally {
		await removeSource(page, b.id);
	}
});

/** The title of the first row that is not scrolled out of view. */
async function rowAtTop(feed: Locator): Promise<string> {
	return feed.evaluate((el) => {
		const top = el.getBoundingClientRect().top;
		const visible = [...el.querySelectorAll('[role=row]')]
			.map((r) => ({ r, y: r.getBoundingClientRect().top }))
			.filter((x) => x.y >= top - 1)
			.sort((a, b) => a.y - b.y)[0];
		return visible?.r.querySelector('.title')?.textContent ?? '';
	});
}

test('follow: F from oldest-first goes to newest-first', async ({ page }) => {
	test.skip(isMobile(page), 'keyboard');
	await open(page, '/?sort=oldest');
	await expect(follow(page)).toHaveCount(0);
	await page.keyboard.press('F');
	await expect(page).not.toHaveURL(/sort=/);
	await expect(follow(page)).toHaveAttribute('data-follow', 'on');
});

// ── Query syntax ──────────────────────────────────────────────

test.describe('query syntax', () => {
	test('tag: and src: change the results and the URL, and back restores them', async ({ page }) => {
		await open(page);
		await expect(rows(page).first()).toBeVisible();

		await query(page, 'tag:rust');
		await submit(page);
		await expect(page).toHaveURL(/\?tag=\d+$/);
		await expect(rows(page)).toHaveCount(1);
		await expect(rows(page).first()).toContainText('Rust for Linux status');
		await expect(search(page)).toHaveValue('tag:rust');

		await query(page, 'src:"Beta Blog" sort:oldest');
		await submit(page);
		await expect(page).toHaveURL(/\?source=\d+&sort=oldest$/);
		await expect(row(page, 'Old post')).toBeVisible();
		await expect(row(page, 'Rust for Linux status')).toHaveCount(0);
		await expect(rows(page).first()).toContainText('Old post');
		// The bar shows the filter as text, with the name quoted.
		await expect(search(page)).toHaveValue('src:"Beta Blog" sort:oldest');

		await page.goBack();
		await expect(page).toHaveURL(/\?tag=\d+$/);
		await expect(rows(page)).toHaveCount(1);
		await expect(search(page)).toHaveValue('tag:rust');
		await page.goBack();
		await expect(page).toHaveURL(/\/$/);
		await expect(search(page)).toHaveValue('');
		await expect(row(page, 'Linux 6.18 released')).toBeVisible();
	});

	test('free text, names in any case, is:unviewed and abbreviations', async ({ page }) => {
		await open(page);
		await query(page, 'linux TAG:RUST src:alp');
		await submit(page);
		await expect(page).toHaveURL(/\?q=linux&source=\d+&tag=\d+$/);
		await expect(rows(page)).toHaveCount(1);
		await expect(search(page)).toHaveValue('linux src:"Alpha News" tag:rust');

		await query(page, 'is:unviewed');
		await submit(page);
		await expect(page).toHaveURL(/\?unviewed=1$/);
	});

	test('unknown names are an inline error, not silently dropped', async ({ page }) => {
		await open(page);
		await query(page, 'tag:nope');
		const err = page.getByTestId('query-error');
		await expect(err).toHaveText('unknown tag: nope');
		await submit(page);
		await expect(err).toBeVisible();
		await expect(page).not.toHaveURL(/tag=/);
		// Still in the bar, so it can be fixed.
		await expect(search(page)).toBeFocused();
		await search(page).fill('tag:rust src:zzz sort:sideways');
		await expect(err).toHaveText('unknown source: zzz');
		await search(page).fill('tag:rust');
		await expect(err).toHaveCount(0);
		await submit(page);
		await expect(page).toHaveURL(/\?tag=\d+$/);
	});

	test('names are completed as you type, including names with spaces', async ({ page }) => {
		await open(page);
		await query(page, 'x src:be');
		const option = page.getByRole('option', { name: /Beta Blog/ });
		await expect(option).toBeVisible();
		// Typing a partial name is not an error.
		await expect(page.getByTestId('query-error')).toHaveCount(0);
		if (isMobile(page)) await option.tap();
		else await page.keyboard.press('Tab');
		await expect(search(page)).toHaveValue('x src:"Beta Blog" ');
		await submit(page);
		await expect(page).toHaveURL(/\?q=x&source=\d+$/);
	});

	test('arrows choose a suggestion and Enter accepts it', async ({ page }) => {
		test.skip(isMobile(page), 'keyboard');
		await open(page);
		await query(page, 'tag:');
		await expect(page.getByRole('option')).toHaveCount(2);
		await page.keyboard.press('ArrowDown');
		await page.keyboard.press('ArrowDown');
		await expect(page.getByRole('option').nth(1)).toHaveAttribute('aria-selected', 'true');
		// Enter takes the highlighted name instead of applying the query.
		await page.keyboard.press('Enter');
		await expect(search(page)).toHaveValue(/^tag:(rust|linux) $/);
		await expect(page).not.toHaveURL(/tag=/);
		await page.keyboard.press('Enter');
		await expect(page).toHaveURL(/tag=\d+/);
	});

	test('old bookmarks with separate parameters still work', async ({ page }) => {
		await open(page, '/?q=Rust&source=1&sort=oldest');
		await expect(rows(page)).toHaveCount(1);
		await expect(search(page)).toHaveValue('Rust src:"Alpha News" sort:oldest');
		// A hand-written query in q is plain text, as it always was.
		await open(page, '/?q=tag:rust');
		await expect(rows(page)).toHaveCount(0);
		await expect(search(page)).toHaveValue('"tag:rust"');
	});

	test('Esc clears the search text but keeps the operators', async ({ page }) => {
		test.skip(isMobile(page), 'keyboard');
		await open(page, '/?q=Linux&sort=oldest');
		await page.keyboard.press('/');
		await page.keyboard.press('Escape');
		await expect(page).toHaveURL(/\?sort=oldest$/);
		await expect(search(page)).toHaveValue('sort:oldest');
	});
});

// ── Load older ────────────────────────────────────────────────

test('load older: reaching the end fetches the next page', async ({ page }) => {
	const b = await addBulk(page, 'older', 230);
	try {
		await open(page, `/?source=${b.id}`);
		// The stream's snapshot is the newest 200.
		await expect(feedEl(page)).toHaveAttribute('aria-rowcount', '200');
		await expect(titled(page, b.title(1))).toBeVisible();

		if (isMobile(page)) await feedEl(page).evaluate((el) => (el.scrollTop = el.scrollHeight));
		else await page.keyboard.press('G');
		await expect(feedEl(page)).toHaveAttribute('aria-rowcount', '230');

		// To the very end: the last row is there, with an end row after it.
		await feedEl(page).evaluate((el) => (el.scrollTop = el.scrollHeight));
		await expect(titled(page, b.title(230))).toBeVisible();
		await expect(page.getByTestId('older')).toHaveText('— end —');
		// Nothing more is requested once the end is reached.
		await page.waitForTimeout(300);
		await expect(feedEl(page)).toHaveAttribute('aria-rowcount', '230');

		// The seam between the snapshot and the older page: no gap, no duplicate.
		const rowH = isMobile(page) ? 44 : 20;
		await feedEl(page).evaluate((el, y) => (el.scrollTop = y), 190 * rowH);
		await expect(titled(page, b.title(201))).toBeVisible();
		const numbers = await feedEl(page).evaluate((el) =>
			[...el.querySelectorAll('[role=row] .title')].map((t) => Number((t.textContent ?? '').split(' ').pop()))
		);
		expect(numbers).toContain(200);
		expect(numbers).toContain(201);
		for (let i = 1; i < numbers.length; i++) expect(numbers[i]).toBe(numbers[i - 1]! + 1);
	} finally {
		await removeSource(page, b.id);
	}
});

test('load older: a push while older pages are loaded keeps the list intact', async ({ page }) => {
	const b = await addBulk(page, 'older-live', 230);
	try {
		await open(page, `/?source=${b.id}`);
		await expect(feedEl(page)).toHaveAttribute('aria-rowcount', '200');
		if (isMobile(page)) await feedEl(page).evaluate((el) => (el.scrollTop = el.scrollHeight));
		else await page.keyboard.press('G');
		await expect(feedEl(page)).toHaveAttribute('aria-rowcount', '230');

		await pushBulk(page, b, `${b.slug} fresh`);
		await expect(feedEl(page)).toHaveAttribute('aria-rowcount', '231');
		await feedEl(page).evaluate((el) => (el.scrollTop = el.scrollHeight));
		await expect(titled(page, b.title(230))).toBeVisible();
		await expect(page.getByTestId('older')).toHaveText('— end —');
	} finally {
		await removeSource(page, b.id);
	}
});

test('load older: a small list ends at once', async ({ page }) => {
	await open(page, '/?sort=oldest');
	await expect(page.getByTestId('older')).toHaveText('— end —');
});

// ── Help ──────────────────────────────────────────────────────

test('help: opens and closes, listing the keys of the view', async ({ page }) => {
	await open(page);
	const help = page.getByRole('dialog', { name: 'Keys: feed' });
	if (isMobile(page)) await page.getByTestId('open-help').tap();
	else await page.keyboard.press('?');
	await expect(help).toBeVisible();
	await expect(help).toContainText('follow: jump to the newest and stick to it');
	await expect(help).toContainText('tag:<name>');
	await expect(help).toContainText(':sort [newest|oldest|score]');
	if (!isMobile(page)) {
		// The feed's keys do nothing underneath the overlay.
		await page.keyboard.press('o');
		await expect(page).not.toHaveURL(/sort=/);
		await page.keyboard.press('q');
	} else {
		await page.getByTestId('help-close').tap();
	}
	await expect(help).toHaveCount(0);
	if (!isMobile(page)) {
		await page.keyboard.press('?');
		await expect(help).toBeVisible();
		await page.keyboard.press('Escape');
		await expect(help).toHaveCount(0);
		// Back in the feed.
		await page.keyboard.press('o');
		await expect(page).toHaveURL(/sort=oldest/);
	}
});

test('help: a management view lists its own keys', async ({ page }) => {
	await openManage(page, 'tags');
	const help = page.getByRole('dialog', { name: 'Keys: tags' });
	if (isMobile(page)) await page.getByTestId('open-help').tap();
	else await page.keyboard.press('?');
	await expect(help).toBeVisible();
	await expect(help).toContainText('delete the selected row');
	// Tags cannot be enabled or refreshed, so those keys are not listed.
	await expect(help).not.toContainText('enable or disable');
	await expect(help).toContainText(':refresh [source]');
	if (isMobile(page)) await page.getByTestId('help-close').tap();
	else await page.keyboard.press('q');
	await expect(help).toHaveCount(0);
	// q closed the help, not the view.
	await expect(page).toHaveURL(/\/tags$/);
});

// ── Command line ──────────────────────────────────────────────

test.describe('command line', () => {
	test.skip(({ isMobile }) => isMobile, 'keyboard');

	async function command(page: Page, text: string) {
		await page.keyboard.press(':');
		await page.getByTestId('command').fill(text);
		await page.keyboard.press('Enter');
	}

	test('filter commands change the URL', async ({ page }) => {
		await open(page);
		await command(page, 'sort oldest');
		await expect(page).toHaveURL(/\?sort=oldest$/);
		await command(page, 'sort');
		await expect(page).not.toHaveURL(/sort=/);
		await command(page, 'tag rust');
		await expect(page).toHaveURL(/\?tag=\d+$/);
		await command(page, 'src beta blog');
		await expect(page).toHaveURL(/\?source=\d+&tag=\d+$/);
		await command(page, 'tag all');
		await command(page, 'src all');
		await command(page, 'unviewed on');
		await expect(page).toHaveURL(/\?unviewed=1$/);
		await command(page, 'unviewed');
		await expect(page).not.toHaveURL(/unviewed/);
	});

	test('errors stay in the line, and :help opens the help', async ({ page }) => {
		await open(page);
		await page.keyboard.press(':');
		await page.getByTestId('command').fill('tag nope');
		await page.keyboard.press('Enter');
		await expect(page.getByTestId('command-line')).toContainText('unknown tag: nope');
		await page.getByTestId('command').fill('sort sideways');
		await page.keyboard.press('Enter');
		await expect(page.getByTestId('command-line')).toContainText('sort: newest, oldest or score');
		await page.getByTestId('command').fill('help');
		await page.keyboard.press('Enter');
		await expect(page.getByRole('dialog', { name: 'Keys: feed' })).toBeVisible();
		await page.keyboard.press('q');
		await expect(page.getByRole('dialog')).toHaveCount(0);
	});

	test('Tab completes commands and names, ↑ and ↓ walk the history', async ({ page }) => {
		await open(page);
		await page.keyboard.press(':');
		const input = page.getByTestId('command');
		await input.pressSequentially('sor');
		await page.keyboard.press('Tab');
		await expect(input).toHaveValue('sort ');
		await page.keyboard.press('Tab');
		await expect(input).toHaveValue('sort newest');
		await page.keyboard.press('Tab');
		await expect(input).toHaveValue('sort oldest');
		await page.keyboard.press('Enter');
		await expect(page).toHaveURL(/sort=oldest/);

		// Names, with their spaces.
		await page.keyboard.press(':');
		await input.pressSequentially('src al');
		await page.keyboard.press('Tab');
		await expect(input).toHaveValue('src Alpha News');
		await page.keyboard.press('Enter');
		await expect(page).toHaveURL(/source=\d+&sort=oldest/);

		// Several candidates: their common prefix first, then a list.
		await page.keyboard.press(':');
		await input.pressSequentially('s');
		await page.keyboard.press('Tab');
		await expect(input).toHaveValue('sources');
		await expect(page.getByTestId('command-hints')).toContainText('sort');
		await page.keyboard.press('Tab');
		await expect(input).toHaveValue('sort');
		await page.keyboard.press('Escape');

		// History: the newest first, then back.
		await page.keyboard.press(':');
		await input.pressSequentially('par');
		await page.keyboard.press('ArrowUp');
		await expect(input).toHaveValue('src Alpha News');
		await page.keyboard.press('ArrowUp');
		await expect(input).toHaveValue('sort oldest');
		await page.keyboard.press('ArrowDown');
		await expect(input).toHaveValue('src Alpha News');
		await page.keyboard.press('ArrowDown');
		await expect(input).toHaveValue('par');
		await page.keyboard.press('Escape');
	});

	test(':refresh and :time', async ({ page }) => {
		await open(page);
		await command(page, 'refresh');
		await expect(page.getByTestId('status')).toContainText('refreshing all sources');
		await command(page, 'refresh alpha news');
		await expect(page.getByTestId('status')).toContainText('refreshing source');
		await command(page, 'time relative');
		await expect(page.getByTestId('status')).toContainText('times: relative');
		await expect(rows(page).first().locator('.date.full')).toContainText(/ago$/);
		await command(page, 'time');
		await expect(rows(page).first().locator('.date.full')).toHaveText(/^\d\d\.\d\d \d\d:\d\d$/);
	});

	test('filter commands work from a management view, and :follow goes to the feed', async ({ page }) => {
		await openManage(page, 'sources');
		await command(page, 'tag rust');
		await expect(page).toHaveURL(/\/\?tag=\d+$/);
		await expect(rows(page)).toHaveCount(1);
		await openManage(page, 'tags');
		await command(page, 'follow');
		await expect(page).toHaveURL(/\/\??/);
		await expect(page.getByTestId('feed')).toBeVisible();
	});
});

// ── Pickers ───────────────────────────────────────────────────

test.describe('pickers', () => {
	test.skip(({ isMobile }) => isMobile, 'keyboard; on a phone the filter sheet does this');

	test('T picks a tag by typing part of its name', async ({ page }) => {
		await open(page);
		await page.keyboard.press('T');
		const picker = page.getByRole('dialog', { name: 'Pick a tag' });
		await expect(picker).toBeVisible();
		await expect(picker.getByRole('option')).toHaveCount(3);
		await expect(picker.getByRole('option', { name: 'rust' })).toBeVisible();
		await expect(picker.getByRole('option', { name: 'linux' })).toBeVisible();
		await page.keyboard.type('ru');
		await expect(picker.getByRole('option')).toHaveText(['rust']);
		await page.keyboard.press('Enter');
		await expect(picker).toHaveCount(0);
		await expect(page).toHaveURL(/\?tag=\d+$/);
		await expect(rows(page)).toHaveCount(1);
		await expect(rows(page).first()).toContainText('Rust for Linux status');
	});

	test('S picks a source, the arrows move, Esc closes without picking', async ({ page }) => {
		await open(page);
		await page.keyboard.press('S');
		const picker = page.getByRole('dialog', { name: 'Pick a source' });
		await expect(picker.getByRole('option')).toHaveText(['all', 'Alpha News', 'Beta Blog']);
		await expect(picker.getByRole('option').first()).toHaveAttribute('aria-selected', 'true');
		await page.keyboard.press('ArrowDown');
		await page.keyboard.press('ArrowDown');
		await expect(picker.getByRole('option').nth(2)).toHaveAttribute('aria-selected', 'true');
		await page.keyboard.press('Enter');
		await expect(page).toHaveURL(/\?source=\d+$/);
		await expect(row(page, 'Go 1.26.8 security release')).toBeVisible();
		await expect(row(page, 'Linux 6.18 released')).toHaveCount(0);

		await page.keyboard.press('S');
		await expect(picker).toBeVisible();
		await page.keyboard.type('zzz');
		await expect(picker).toContainText('no match');
		await page.keyboard.press('Escape');
		await expect(picker).toHaveCount(0);
		await expect(page).toHaveURL(/\?source=\d+$/);

		// "all" clears it again, and a click picks too.
		await page.keyboard.press('S');
		await picker.getByRole('option', { name: 'all' }).click();
		await expect(page).not.toHaveURL(/source=/);
	});

	test('the picker takes typing, not the feed keys', async ({ page }) => {
		await open(page);
		await page.keyboard.press('S');
		await expect(page.getByRole('dialog', { name: 'Pick a source' })).toBeVisible();
		await page.keyboard.type('jko');
		await expect(page).not.toHaveURL(/sort=/);
		await expect(page.getByRole('dialog', { name: 'Pick a source' })).toContainText('no match');
		await page.keyboard.press('Escape');
	});
});

// ── Highlighting ──────────────────────────────────────────────

test('search highlighting marks the words as spans, never as HTML', async ({ page }) => {
	await open(page, '/?q=rust');
	const r = row(page, 'Rust for Linux status');
	await expect(r).toBeVisible();
	const marks = r.locator('mark');
	await expect(marks.first()).toHaveText('Rust');
	await expect(marks.first()).toHaveCSS('color', 'rgb(255, 255, 255)');
	// Only the words: the rest of the title is unmarked text.
	await expect(r.locator('.title')).toHaveText('Rust for Linux status');
	await expect(r.locator('.title mark')).toHaveCount(1);

	// Markup in a description stays text: nothing but <mark> is created.
	await open(page, '/?q=Tracked');
	const evil = row(page, 'Evil item');
	await expect(evil).toBeVisible();
	await expect(evil.locator('mark')).toHaveText('Tracked');
	await expect(evil.locator('mark *')).toHaveCount(0);
	await expect(evil.locator('b, img, script, style')).toHaveCount(0);
	await expect(page.locator('img')).toHaveCount(0);

	// The expanded row marks its description too.
	// (The only row is already selected, so a click would toggle it too.)
	if (isMobile(page)) await evil.tap();
	else await page.keyboard.press(' ');
	await expect(page.getByRole('region', { name: 'item details' }).locator('mark')).toHaveText('Tracked');
});

test('words are matched whole and in any case', async ({ page }) => {
	await open(page, '/?q=LINUX');
	const r = row(page, 'Linux 6.18 released');
	await expect(r.locator('.title mark')).toHaveText('Linux');
	// "Linux" inside the description's "Linux 6.18" is marked as well (desktop shows it).
	await expect(r.locator('mark')).not.toHaveCount(0);
});

// ── Time ──────────────────────────────────────────────────────

test('time: absolute or relative, and the choice persists', async ({ page }) => {
	await open(page);
	const r = row(page, 'Linux 6.18 released');
	const date = isMobile(page) ? r.locator('.date.short') : r.locator('.date.full');
	const absolute = isMobile(page) ? /^\d\d:\d\d$/ : /^\d\d\.\d\d \d\d:\d\d$/;
	const relative = isMobile(page) ? /^\d+[smhd]$/ : /^\d+[smhd] ago$/;
	await expect(date).toHaveText(absolute);

	if (isMobile(page)) {
		await page.getByTestId('open-filters').tap();
		await page.getByTestId('filter-sheet').getByLabel('times').selectOption('relative');
		await page.getByTestId('filter-sheet').getByRole('button', { name: 'done' }).tap();
	} else {
		await page.keyboard.press('D');
		await expect(page.getByTestId('status')).toContainText('times: relative');
	}
	await expect(date).toHaveText(relative);

	// It survives a reload.
	await page.reload();
	await expect(row(page, 'Linux 6.18 released')).toBeVisible();
	await expect(date).toHaveText(relative);

	if (isMobile(page)) {
		await page.getByTestId('open-filters').tap();
		await page.getByTestId('filter-sheet').getByLabel('times').selectOption('absolute');
		await page.getByTestId('filter-sheet').getByRole('button', { name: 'done' }).tap();
	} else {
		await page.keyboard.press('D');
	}
	await expect(date).toHaveText(absolute);
	await page.reload();
	await expect(date).toHaveText(absolute);
});

test('time: works without storage', async ({ page }) => {
	test.skip(isMobile(page), 'keyboard');
	await page.addInitScript(() => {
		Object.defineProperty(window, 'localStorage', {
			get() {
				throw new Error('blocked');
			}
		});
	});
	await open(page);
	await page.keyboard.press('D');
	await expect(rows(page).first().locator('.date.full')).toContainText('ago');
});
