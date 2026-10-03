import { expect, test, type Page } from '@playwright/test';
import { FEEDS_URL, BASE_URL } from './feeds.mjs';

// These run against one shared daemon, at a desktop and a mobile viewport
// (see playwright.config.ts). Tests that need unviewed items add their own
// through the feed server, so they don't depend on each other.

const isMobile = (page: Page) => (page.viewportSize()?.width ?? 1000) < 720;

const rows = (page: Page) => page.locator('[role=row]');
const row = (page: Page, title: string) => rows(page).filter({ hasText: title });
const selected = (page: Page) => page.locator('[role=row][aria-selected=true]');

let pushed = 0;

/** Adds an item to a feed, refreshes, and returns its title. */
async function pushItem(page: Page, feed = 'alpha'): Promise<string> {
	const title = `Live ${test.info().project.name} ${Date.now()}-${++pushed}`;
	const add = await page.request.post(`${FEEDS_URL}/add?feed=${feed}&title=${encodeURIComponent(title)}`);
	expect(add.ok()).toBeTruthy();
	return title;
}

async function refreshAll(page: Page) {
	const res = await page.request.post('/api/refresh', {
		data: {},
		headers: { Origin: BASE_URL }
	});
	expect(res.status()).toBe(204);
}

async function unviewedIds(page: Page): Promise<string[]> {
	const res = await page.request.get('/api/items?unviewed=1&limit=500');
	const body = (await res.json()) as { items?: { id: string; title?: string }[] };
	return (body.items ?? []).map((it) => it.title ?? '');
}

async function open(page: Page, path = '/') {
	await page.goto(path);
	await expect(page.getByTestId('status').locator('[data-status]')).toHaveAttribute('data-status', 'connected');
	await expect(row(page, 'Linux 6.18 released')).toBeVisible();
}

test.describe('feed', () => {
	test('renders the TUI columns and connects', async ({ page }) => {
		const errors: string[] = [];
		page.on('console', (m) => {
			if (m.type() === 'error') errors.push(m.text());
		});
		page.on('pageerror', (e) => errors.push(e.message));

		await open(page);
		const r = row(page, 'Rust for Linux status');
		await expect(r).toContainText('[ALP]');
		await expect(r).toContainText('[linux]');
		await expect(r).toContainText('[rust]');
		await expect(r).toContainText('lwn.net');
		// Date column: dd.MM HH:mm on desktop, HH:mm on mobile.
		if (isMobile(page)) {
			await expect(r.locator('.date.short')).toHaveText(/^\d\d:\d\d$/);
		} else {
			await expect(r.locator('.date.full')).toHaveText(/^\d\d\.\d\d \d\d:\d\d$/);
			await expect(r).toContainText('An overview of where Rust support stands.');
		}
		// Validated colors reach the page.
		await expect(r.locator('.src span')).toHaveCSS('color', 'rgb(255, 102, 0)');
		// Newest first.
		const titles = await rows(page).locator('.title').allTextContents();
		expect(titles.indexOf('Linux 6.18 released')).toBeLessThan(titles.indexOf('Old post'));

		await expect(page.getByTestId('status')).toContainText('unviewed');
		// Nothing tripped the CSP or threw.
		expect(errors).toEqual([]);
	});

	test('rows have a fixed height', async ({ page }) => {
		await open(page);
		const box = await rows(page).first().boundingBox();
		expect(box?.height).toBe(isMobile(page) ? 44 : 20);
		// No horizontal scrolling at any width.
		const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
		expect(overflow).toBeLessThanOrEqual(0);
	});

	test('a row with many chips and a long domain stays two lines on mobile', async ({ page }) => {
		// The feed's items are titled "<name> story n" and link to <name>.example,
		// so the name gives them both seeded tags and a long domain; with a long
		// source label the first line is far wider than a phone.
		const name = `rust-linux-a-rather-long-feed-domain-${test.info().project.name}`;
		const headers = { Origin: BASE_URL };
		const res = await page.request.post('/api/sources', {
			headers,
			data: { name, url: `${FEEDS_URL}/extra/${name}.xml`, refresh_sec: 3600, abbreviation: 'A Long Src Label' }
		});
		expect(res.status()).toBe(201);
		const id = ((await res.json()) as { id: string }).id;
		try {
			await page.goto(`/?source=${id}`);
			const r = row(page, `${name} story 1`);
			await expect(r).toContainText('[linux]');
			await expect(r).toContainText('[rust]');
			// Nothing in the row is cut off at the top or bottom.
			const cells = await r.locator('.cells').evaluate((c) => {
				const box = c.getBoundingClientRect();
				// The cells themselves: on desktop .meta has no box (display: contents).
				const kids = [...c.querySelectorAll(':scope > :not(.meta), .meta > *')]
					.map((e) => e.getBoundingClientRect())
					.filter((k) => k.height > 0);
				return { top: box.top, bottom: box.bottom, kids: kids.map((k) => [k.top, k.bottom]) };
			});
			for (const [top, bottom] of cells.kids) {
				expect(top).toBeGreaterThanOrEqual(cells.top);
				expect(bottom).toBeLessThanOrEqual(cells.bottom);
			}
			if (isMobile(page)) {
				// Two lines: the meta line above the title, the domain on the first.
				const meta = await r.locator('.meta').boundingBox();
				const title = await r.locator('.title').boundingBox();
				const domain = await r.locator('.domain').boundingBox();
				expect(meta && title && meta.y + meta.height <= title.y).toBeTruthy();
				expect(domain && meta && domain.y >= meta.y && domain.y + domain.height <= meta.y + meta.height).toBeTruthy();
			}
		} finally {
			await page.request.delete(`/api/sources/${id}`, { headers, data: {} });
		}
	});

	test('treats feed content as untrusted', async ({ page }) => {
		await open(page);
		const evil = row(page, 'Evil item');
		await expect(evil).toBeVisible();
		// Select and expand it.
		if (isMobile(page)) {
			await evil.tap();
		} else {
			await evil.click();
			await page.keyboard.press(' ');
		}
		const detail = page.getByRole('region', { name: 'item details' });
		await expect(detail).toContainText('bold Tracked text');
		// No script ran, script text is not shown, and the javascript: link
		// is not rendered as a link (nor its host as a domain).
		expect(await page.evaluate(() => (window as unknown as { __pwned?: number }).__pwned)).toBeUndefined();
		await expect(detail).not.toContainText('__pwned');
		await expect(detail.locator('a')).toHaveCount(0);
		await expect(page.locator('a[href^="javascript:"]')).toHaveCount(0);
		await expect(page.locator('img')).toHaveCount(0);

		// The tracking pixel was never requested.
		const hits = await (await page.request.get(`${FEEDS_URL}/pixel-hits`)).json();
		expect(hits.hits).toBe(0);
	});

	test('safe links open in a new tab without a referrer', async ({ page }) => {
		await open(page);
		const r = row(page, 'Go 1.26.8 security release');
		if (isMobile(page)) {
			await r.tap();
		} else {
			await r.click();
			await page.keyboard.press('l');
		}
		const link = page.getByRole('link', { name: 'open ↗' });
		await expect(link).toHaveAttribute('href', 'https://go.dev/blog/go1.26.8');
		await expect(link).toHaveAttribute('target', '_blank');
		await expect(link).toHaveAttribute('rel', 'noopener noreferrer');
		await expect(link).toHaveAttribute('referrerpolicy', 'no-referrer');
	});

	test('live pushes appear without a reload', async ({ page }) => {
		await open(page);
		const title = await pushItem(page);
		await refreshAll(page);
		const r = row(page, title);
		await expect(r).toBeVisible();
		// Newest first: it is the first row.
		await expect(rows(page).first()).toContainText(title);
	});

	test('rows the cursor goes through are read, dimmed and sent in batches', async ({ page }) => {
		const first = await pushItem(page, 'beta');
		const second = await pushItem(page, 'beta');
		await refreshAll(page);
		await expect.poll(() => unviewedIds(page)).toEqual(expect.arrayContaining([first, second]));
		await open(page);
		const r1 = row(page, first);
		const r2 = row(page, second);
		await expect(r1).toBeVisible();
		await expect(r2).toBeVisible();
		// On screen is not read: well past a batch, both are still unviewed.
		await page.waitForTimeout(4000);
		await expect(r1).not.toHaveClass(/\bviewed\b/);
		expect(await unviewedIds(page)).toEqual(expect.arrayContaining([first, second]));

		// Selecting a row reads it: dimmed at once, sent with the next batch.
		await r1.click();
		await expect(r1).toHaveClass(/\bviewed\b/);
		await expect(r1.locator('.dot')).toHaveText('');
		await expect(r1.locator('.cells')).toHaveCSS('opacity', '0.75');
		await expect.poll(() => unviewedIds(page), { timeout: 8000 }).not.toContain(first);
		// The other row is untouched.
		await expect(r2).not.toHaveClass(/\bviewed\b/);
		expect(await unviewedIds(page)).toContain(second);
	});

	test('stepping with j/k reads the row stepped off and the row stepped on', async ({ page }) => {
		test.skip(isMobile(page), 'keyboard');
		// The two newest rows; they can share a second, so either order.
		const titles = [await pushItem(page, 'beta'), await pushItem(page, 'beta')];
		await refreshAll(page);
		await expect.poll(() => unviewedIds(page)).toEqual(expect.arrayContaining(titles));
		await open(page);
		const top = rows(page).nth(0);
		const next = rows(page).nth(1);
		await expect(top).toContainText(/Live /);
		await expect(next).toContainText(/Live /);
		// The page opens on the first row without reading it.
		await expect(selected(page)).toHaveAttribute('data-id', (await top.getAttribute('data-id'))!);
		await expect(top).not.toHaveClass(/\bviewed\b/);
		await expect(next).not.toHaveClass(/\bviewed\b/);
		await page.keyboard.press('j');
		await expect(top).toHaveClass(/\bviewed\b/);
		await expect(next).toHaveAttribute('aria-selected', 'true');
		await expect(next).toHaveClass(/\bviewed\b/);
		await expect.poll(() => unviewedIds(page), { timeout: 8000 }).not.toContain(titles[0]);
		await expect.poll(() => unviewedIds(page)).not.toContain(titles[1]);
	});

	test('pending views are sent when the page goes away', async ({ page }) => {
		const title = await pushItem(page);
		await refreshAll(page);
		// Wait for the fetch to land before loading the page.
		await expect.poll(() => unviewedIds(page)).toContain(title);
		// Freeze the page's timers so the 3s batch can never go out: only
		// the pagehide beacon can mark the item viewed. (Playwright doesn't
		// report requests an unloading page makes, so check the result.)
		await page.clock.install();
		await open(page);
		const r = row(page, title);
		await expect(r).toBeVisible();
		// Selecting it reads it.
		await r.click();
		await expect(r).toHaveClass(/\bviewed\b/);
		await page.goto('about:blank');
		await expect.poll(() => unviewedIds(page)).not.toContain(title);
	});

	test('filters live in the URL', async ({ page }) => {
		await open(page, '/?sort=oldest');
		await expect(rows(page).first()).toContainText('Old post');
		await page.goto('/?q=kernel');
		await expect(page.getByText('no items')).toBeVisible();
		await expect(rows(page)).toHaveCount(0);
		await page.goto('/?q=Rust');
		await expect(rows(page)).toHaveCount(1);
		await expect(rows(page).first()).toContainText('Rust for Linux status');
		// The back button restores the previous filter.
		await page.goBack();
		await expect(page).toHaveURL(/\?q=kernel$/);
	});
});

test.describe('keyboard', () => {
	test.skip(({ isMobile }) => isMobile, 'desktop only');

	test('moves, expands and opens like the TUI', async ({ page }) => {
		await open(page);
		await expect(selected(page)).toContainText(await rows(page).first().locator('.title').innerText());
		await page.keyboard.press('j');
		await page.keyboard.press('j');
		const third = await rows(page).nth(2).locator('.title').innerText();
		await expect(selected(page)).toContainText(third);
		await page.keyboard.press('k');
		await expect(selected(page)).toContainText(await rows(page).nth(1).locator('.title').innerText());
		await page.keyboard.press('G');
		await expect(selected(page)).toContainText(await rows(page).last().locator('.title').innerText());
		await page.keyboard.press('g');
		await expect(rows(page).first()).toHaveAttribute('aria-selected', 'true');
		await page.keyboard.press('d');
		await expect(rows(page).first()).toHaveAttribute('aria-selected', 'false');
		await page.keyboard.press('u');
		await expect(rows(page).first()).toHaveAttribute('aria-selected', 'true');

		// Expand and collapse.
		await page.keyboard.press(' ');
		await expect(page.getByRole('region', { name: 'item details' })).toBeVisible();
		await page.keyboard.press('Escape');
		await expect(page.getByRole('region', { name: 'item details' })).toHaveCount(0);

		// Enter opens the link in a new tab.
		await page.context().route('https://go.dev/**', (route) =>
			route.fulfill({ contentType: 'text/html', body: '<p>go.dev</p>' })
		);
		const g = row(page, 'Go 1.26.8 security release');
		await g.click();
		const [popup] = await Promise.all([page.waitForEvent('popup'), page.keyboard.press('Enter')]);
		await popup.waitForURL('https://go.dev/blog/go1.26.8');
		// noopener and noreferrer: the new tab cannot reach back.
		expect(await popup.evaluate(() => window.opener === null && document.referrer === '')).toBe(true);
		await popup.close();
	});

	test('search: / focuses, Enter applies, Esc clears', async ({ page }) => {
		await open(page);
		await page.keyboard.press('/');
		const input = page.getByTestId('search');
		await expect(input).toBeFocused();
		// Typing doesn't trigger feed keys.
		await page.keyboard.type('golang security');
		await expect(page).not.toHaveURL(/q=/);
		await input.fill('security');
		await page.keyboard.press('Enter');
		await expect(page).toHaveURL(/\?q=security$/);
		await expect(rows(page)).toHaveCount(1);
		await expect(rows(page).first()).toContainText('Go 1.26.8 security release');
		await expect(input).not.toBeFocused();

		await page.keyboard.press('/');
		await page.keyboard.press('Escape');
		await expect(page).not.toHaveURL(/q=/);
		await expect(input).toHaveValue('');
		await expect(row(page, 'Linux 6.18 released')).toBeVisible();
	});

	test('s, t and o change the filter', async ({ page }) => {
		await open(page);
		await page.keyboard.press('s');
		await expect(page).toHaveURL(/source=1/);
		await expect(page.getByRole('button', { name: /src:/ })).toContainText('Alpha News');
		await expect(row(page, 'Go 1.26.8 security release')).toHaveCount(0);
		await page.keyboard.press('s');
		await expect(page).toHaveURL(/source=2/);
		await expect(row(page, 'Go 1.26.8 security release')).toBeVisible();
		await page.keyboard.press('s');
		await expect(page).not.toHaveURL(/source=/);

		await page.keyboard.press('t');
		await expect(page).toHaveURL(/tag=\d+/);
		await page.keyboard.press('t');
		await page.keyboard.press('t');
		await expect(page).not.toHaveURL(/tag=/);

		await page.keyboard.press('o');
		await expect(page).toHaveURL(/sort=oldest/);
		await expect(rows(page).first()).toContainText('Old post');
		await page.keyboard.press('o');
		await expect(page).not.toHaveURL(/sort=/);
	});

	test('r refreshes all sources', async ({ page }) => {
		await open(page);
		await page.keyboard.press('r');
		await expect(page.getByTestId('status')).toContainText('refreshing all sources');
	});
});

test.describe('mobile', () => {
	test.skip(({ isMobile }) => !isMobile, 'mobile only');

	test('filter sheet changes the filter', async ({ page }) => {
		await open(page);
		await page.getByTestId('open-filters').tap();
		const sheet = page.getByTestId('filter-sheet');
		await expect(sheet).toBeVisible();
		await sheet.getByLabel('sort').selectOption('oldest');
		await expect(page).toHaveURL(/sort=oldest/);
		await expect(rows(page).first()).toContainText('Old post');
		await sheet.getByLabel('source').selectOption({ label: 'Beta Blog' });
		await expect(page).toHaveURL(/source=2/);
		await sheet.getByRole('button', { name: 'done' }).tap();
		await expect(sheet).toHaveCount(0);
	});

	test('search input does not zoom (16px)', async ({ page }) => {
		await open(page);
		await expect(page.getByTestId('search')).toHaveCSS('font-size', '16px');
	});
});

test.describe('security', () => {
	test('headers and CSP', async ({ page }) => {
		const res = await page.goto('/');
		const h = res!.headers();
		expect(h['content-security-policy']).toMatch(/default-src 'none';.* script-src 'self' 'nonce-[^']+'/);
		expect(h['content-security-policy']).toContain("frame-ancestors 'none'");
		expect(h['x-content-type-options']).toBe('nosniff');
		expect(h['referrer-policy']).toBe('no-referrer');
		expect(h['cross-origin-opener-policy']).toBe('same-origin');
		expect(h['cache-control']).toBe('no-cache');
	});

	test('cross-origin mutations are refused', async ({ page }) => {
		const evil = await page.request.post('/api/refresh', {
			data: {},
			headers: { Origin: 'https://evil.example' }
		});
		expect(evil.status()).toBe(403);
		const form = await page.request.post('/api/viewed', {
			headers: { Origin: BASE_URL, 'Content-Type': 'application/x-www-form-urlencoded' },
			data: 'ids=1'
		});
		expect(form.status()).toBe(415);
	});
});
