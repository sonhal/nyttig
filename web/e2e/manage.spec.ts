import { expect, test, type Locator, type Page } from '@playwright/test';
import { BASE_URL, FEEDS_URL } from './feeds.mjs';

// The management views against the real daemon, at the desktop viewport
// (keyboard) and the mobile one (touch). Both projects share one daemon, so
// every test works on sources, tags and rules it creates itself, and puts
// back anything shared that it changes.

const isMobile = (page: Page) => (page.viewportSize()?.width ?? 1000) < 720;

type Tool = 'add' | 'edit' | 'delete' | 'toggle' | 'refresh';
const KEYS: Record<Tool, string> = { add: 'a', edit: 'e', delete: 'x', toggle: ' ', refresh: 'r' };

/** Runs a toolbar action: its key on desktop, a tap on its button on mobile. */
async function act(page: Page, tool: Tool) {
	if (isMobile(page)) await page.getByTestId('tool-' + tool).tap();
	else await page.keyboard.press(KEYS[tool]);
}

/** Selects a row: a click on desktop, a tap on mobile. */
async function pick(row: Locator) {
	if (isMobile(row.page())) await row.tap();
	else await row.click();
	await expect(row).toHaveAttribute('aria-selected', 'true');
}

/** Saves the open form: Enter in a text field on desktop, the button on mobile. */
async function save(page: Page, field: Locator) {
	if (isMobile(page)) await page.getByTestId('save').tap();
	else {
		await field.focus();
		await page.keyboard.press('Enter');
	}
}

/** Confirms a delete: y on desktop, the button on mobile. */
async function confirmDelete(page: Page) {
	if (isMobile(page)) await page.getByTestId('confirm-delete').tap();
	else await page.keyboard.press('y');
}

const listRows = (page: Page, view: string) => page.getByTestId(view + '-list').locator('[role=row]');
const listRow = (page: Page, view: string, text: string) => listRows(page, view).filter({ hasText: text });

async function openFeed(page: Page) {
	await page.goto('/');
	await expect(page.getByTestId('status').locator('[data-status]')).toHaveAttribute('data-status', 'connected');
}

/** Goes from the feed to a management view the way a user would. */
async function goTo(page: Page, view: 'sources' | 'tags' | 'rules') {
	if (isMobile(page)) {
		await page.getByTestId('open-filters').tap();
		await page.getByTestId('manage-' + view).tap();
	} else {
		await page.keyboard.press(':');
		await page.keyboard.type(view.slice(0, 2));
		await page.keyboard.press('Enter');
	}
	await expect(page).toHaveURL(new RegExp(`/${view}$`));
	await expect(page.getByTestId(view + '-list')).toBeVisible();
}

/** Leaves a management view for the feed: q on desktop, the tab on mobile. */
async function backToFeed(page: Page) {
	if (isMobile(page)) await page.getByTestId('nav-feed').tap();
	else await page.keyboard.press('q');
	await expect(page.getByTestId('feed')).toBeVisible();
}

test('sources: add, edit, enable/disable, refresh, fetch errors and delete', async ({ page }) => {
	const slug = `gamma-${test.info().project.name}`;
	const name = `Gamma ${test.info().project.name}`;
	await openFeed(page);
	await goTo(page, 'sources');
	await expect(listRow(page, 'sources', 'Alpha News')).toBeVisible();

	// Add, with a validation error first.
	await act(page, 'add');
	const form = page.getByTestId('source-form');
	await expect(page.getByTestId('source-name')).toBeFocused();
	await page.getByTestId('source-name').fill(name);
	await page.getByTestId('source-url').fill('ftp://example.com/feed');
	await save(page, page.getByTestId('source-url'));
	await expect(form).toContainText('url must be http or https');
	await page.getByTestId('source-url').fill(`${FEEDS_URL}/extra/${slug}.xml`);
	await page.getByTestId('source-refresh').fill('2h');
	await page.getByTestId('source-abbreviation').fill('GAM');
	if (isMobile(page)) await expect(page.getByTestId('source-url')).toHaveCSS('font-size', '16px');
	await save(page, page.getByTestId('source-url'));
	await expect(form).toHaveCount(0);

	// The new source is enabled and fetched straight away.
	const row = listRow(page, 'sources', name);
	await expect(row).toBeVisible();
	await expect(row).toHaveAttribute('aria-selected', 'true');
	await expect(row).toContainText('●');
	await expect(row).toContainText('[GAM]');
	if (!isMobile(page)) await expect(row).toContainText('2h');
	await expect(row.locator('.last')).not.toContainText('never', { timeout: 15_000 });
	await expect(row.locator('.next')).toContainText(/in (1h|2h|1\d\dm)/);
	expect((await row.boundingBox())?.height).toBe(isMobile(page) ? 44 : 20);

	// Edit: abbreviation and color.
	await pick(row);
	await act(page, 'edit');
	await expect(page.getByTestId('source-name')).toHaveValue(name);
	await page.getByTestId('source-abbreviation').fill('GMA');
	await page.locator('#src-color').fill('#00FF00');
	await save(page, page.getByTestId('source-abbreviation'));
	await expect(row).toContainText('[GMA]');
	await expect(row.locator('.chip span')).toHaveCSS('color', 'rgb(0, 255, 0)');

	// The feed shows the new abbreviation and color.
	await backToFeed(page);
	const item = page.locator('[role=row]').filter({ hasText: `${slug} story 1` });
	await expect(item).toContainText('[GMA]');
	await expect(item.locator('.src span')).toHaveCSS('color', 'rgb(0, 255, 0)');
	await goTo(page, 'sources');

	// Disable and enable.
	await pick(row);
	await act(page, 'toggle');
	await expect(row).toContainText('○');
	await expect(row.locator('.next')).toHaveText('next off');
	await act(page, 'toggle');
	await expect(row).toContainText('●');

	await act(page, 'refresh');
	await expect(page.getByTestId('note')).toContainText(`refreshing ${name}`);

	// A broken URL shows the fetch error in the error color.
	await act(page, 'edit');
	await page.getByTestId('source-url').fill(`${FEEDS_URL}/missing-${slug}.xml`);
	await save(page, page.getByTestId('source-url'));
	const ferr = row.getByTestId('fetch-error');
	await expect(ferr).toContainText('404', { timeout: 15_000 });
	await expect(ferr).toHaveCSS('color', 'rgb(244, 71, 71)');

	// Delete: cancel once, then confirm. The confirmation says what goes too.
	await act(page, 'delete');
	const confirm = page.getByTestId('confirm');
	await expect(confirm).toContainText(`delete source ${name}?`);
	await expect(confirm).toContainText('This also deletes its 2 items and its 0 rules for this source.');
	if (isMobile(page)) await confirm.getByRole('button', { name: 'cancel' }).tap();
	else await page.keyboard.press('n');
	await expect(confirm).toHaveCount(0);
	await expect(row).toBeVisible();
	await act(page, 'delete');
	await expect(confirm).toContainText('2 items');
	await confirmDelete(page);
	await expect(row).toHaveCount(0);
	await expect(page.getByTestId('note')).toContainText(`deleted ${name}`);
});

test('sources: a kev source reads a CISA KEV catalogue', async ({ page }) => {
	const slug = `kev-${test.info().project.name}`;
	const name = `KEV ${test.info().project.name}`;
	await openFeed(page);
	await goTo(page, 'sources');

	await act(page, 'add');
	const form = page.getByTestId('source-form');
	await form.locator('select').selectOption('kev');
	await expect(form).toContainText("leave empty for CISA's catalogue");
	await expect(page.getByTestId('source-url')).toHaveAttribute('placeholder', /known_exploited_vulnerabilities\.json$/);
	await page.getByTestId('source-name').fill(name);
	await page.getByTestId('source-url').fill(`${FEEDS_URL}/kev/${slug}.json`);
	await save(page, page.getByTestId('source-url'));
	await expect(form).toHaveCount(0);

	const row = listRow(page, 'sources', name);
	await expect(row).toBeVisible();
	await expect(row.locator('.last')).not.toContainText('never', { timeout: 15_000 });
	await expect(row.getByTestId('fetch-error')).toHaveCount(0);
	const id = await row.getAttribute('data-id');

	// One item per CVE added in the last 30 days; the 40-day-old entry is left out.
	await page.goto(`/?source=${id}`);
	const items = page.locator('[role=row]');
	await expect(items.filter({ hasText: `${slug} today vulnerability` })).toBeVisible();
	await expect(items.filter({ hasText: `${slug} recent vulnerability` })).toBeVisible();
	await expect(items.filter({ hasText: 'CVE-2026-9001' })).toBeVisible();
	await expect(items.filter({ hasText: `${slug} old vulnerability` })).toHaveCount(0);

	await goTo(page, 'sources');
	await pick(row);
	await act(page, 'delete');
	await expect(page.getByTestId('confirm')).toContainText('2 items');
	await confirmDelete(page);
	await expect(row).toHaveCount(0);
});

test('tags: add, recolor and delete, and the feed follows', async ({ page }) => {
	const name = `e2e-${test.info().project.name}`;
	await openFeed(page);
	await goTo(page, 'tags');

	await act(page, 'add');
	await page.getByTestId('tag-name').fill(name);
	await page.locator('#tag-color').fill('#123456');
	await save(page, page.getByTestId('tag-name'));
	const row = listRow(page, 'tags', name);
	await expect(row).toBeVisible();
	await expect(row.locator('.chip span')).toHaveCSS('color', 'rgb(18, 52, 86)');
	await expect(row).toContainText('0 rules');

	// A duplicate name is refused by the daemon, and the form says why.
	await act(page, 'add');
	await page.getByTestId('tag-name').fill(name);
	await save(page, page.getByTestId('tag-name'));
	await expect(page.getByTestId('tag-form')).toContainText('already exists');
	if (isMobile(page)) await page.getByTestId('tag-form').getByRole('button', { name: 'cancel' }).tap();
	else await page.keyboard.press('Escape');
	await expect(page.getByTestId('tag-form')).toHaveCount(0);

	// Recolor.
	await pick(row);
	await act(page, 'edit');
	await page.locator('#tag-color').fill('#ABCDEF');
	await save(page, page.getByTestId('tag-name'));
	await expect(row.locator('.chip span')).toHaveCSS('color', 'rgb(171, 205, 239)');

	// Delete: the confirmation spells out the cascade.
	await act(page, 'delete');
	await expect(page.getByTestId('confirm')).toContainText('This also deletes its 0 rules and removes it from 0 items.');
	await confirmDelete(page);
	await expect(row).toHaveCount(0);

	// Recoloring a tag that is on items shows in the feed without a
	// reload of the stream; the shared tag is put back afterwards.
	const linux = listRow(page, 'tags', 'linux');
	await pick(linux);
	await act(page, 'edit');
	const original = await page.locator('#tag-color').inputValue();
	await page.locator('#tag-color').fill('#00FF00');
	await save(page, page.getByTestId('tag-name'));
	await expect(linux.locator('.chip span')).toHaveCSS('color', 'rgb(0, 255, 0)');
	try {
		await backToFeed(page);
		const chip = page
			.locator('[role=row]')
			.filter({ hasText: 'Linux 6.18 released' })
			.locator('.tags .chip span', { hasText: 'linux' });
		await expect(chip).toHaveCSS('color', 'rgb(0, 255, 0)');
	} finally {
		const tags = (await (await page.request.get('/api/tags')).json()) as { tags: { id: string; name: string }[] };
		const id = tags.tags.find((t) => t.name === 'linux')?.id;
		const res = await page.request.patch(`/api/tags/${id}`, {
			data: { color: original },
			headers: { Origin: BASE_URL }
		});
		expect(res.status()).toBe(200);
	}
});

test('tags: a parent tag shows its children\'s items, and deleting it keeps them', async ({ page }) => {
	const parent = `e2e-parent-${test.info().project.name}`;
	await openFeed(page);
	await goTo(page, 'tags');

	// Create the parent, then put the shared "linux" tag under it.
	await act(page, 'add');
	await page.getByTestId('tag-name').fill(parent);
	await save(page, page.getByTestId('tag-name'));
	const parentRow = listRow(page, 'tags', parent);
	await expect(parentRow).toBeVisible();

	const tagsJson = async () =>
		((await (await page.request.get('/api/tags')).json()) as { tags: { id: string; name: string; parent_ids?: string[] }[] }).tags;
	const linux = listRow(page, 'tags', 'linux');
	try {
		await pick(linux);
		await act(page, 'edit');
		// The tag itself is not offered as its own parent.
		await expect(page.getByTestId('tag-parents').getByLabel('linux')).toHaveCount(0);
		await page.getByTestId('tag-parents').getByLabel(parent).check();
		await save(page, page.getByTestId('tag-name'));
		await expect(page.getByTestId('tag-form')).toHaveCount(0);

		// The list is a tree now: linux sits indented under its parent.
		const rows = await listRows(page, 'tags').allTextContents();
		const at = rows.findIndex((r) => r.includes(parent));
		expect(rows[at + 1]).toContain('linux');

		// Filtering by the parent shows the items of its child.
		const all = await tagsJson();
		const parentId = all.find((t) => t.name === parent)?.id;
		expect(all.find((t) => t.name === 'linux')?.parent_ids).toEqual([parentId]);
		await page.goto(`/?tag=${parentId}`);
		await expect(page.getByTestId('status').locator('[data-status]')).toHaveAttribute('data-status', 'connected');
		const row = (title: string) => page.locator('[role=row]').filter({ hasText: title });
		await expect(row('Linux 6.18 released')).toBeVisible();
		await expect(row('Go 1.26.8 security release')).toHaveCount(0);
		// The chips are the item's own tags: no ancestor chip.
		await expect(row('Linux 6.18 released').locator('.tags')).not.toContainText(parent);

		// Deleting the parent counts only its own assignments (none), says
		// its child becomes top-level, and keeps the child and its items.
		await goTo(page, 'tags');
		await pick(listRow(page, 'tags', parent).first());
		await act(page, 'delete');
		const confirm = page.getByTestId('confirm');
		await expect(confirm).toContainText('removes it from 0 items');
		await expect(confirm).toContainText('1 child tag becomes top-level');
		await confirmDelete(page);
		await expect(listRow(page, 'tags', parent)).toHaveCount(0);
		await expect(linux).toHaveCount(1);
		expect((await tagsJson()).find((t) => t.name === 'linux')?.parent_ids ?? []).toEqual([]);
	} finally {
		// Put the shared tag back if the test failed before the delete.
		const all = await tagsJson();
		const id = all.find((t) => t.name === 'linux')?.id;
		if (id && (all.find((t) => t.name === 'linux')?.parent_ids ?? []).length > 0) {
			await page.request.patch(`/api/tags/${id}`, { data: { parent_ids: [] }, headers: { Origin: BASE_URL } });
		}
		const leftover = all.find((t) => t.name === parent)?.id;
		if (leftover) await page.request.delete(`/api/tags/${leftover}`, { headers: { Origin: BASE_URL } });
	}
});

test('rules: live preview with Go syntax, add, edit and delete', async ({ page }) => {
	const project = test.info().project.name;
	await openFeed(page);
	await goTo(page, 'rules');
	await expect(listRow(page, 'rules', '(?i)\\brust\\b')).toBeVisible();

	await act(page, 'add');
	const form = page.getByTestId('rule-form');
	const pattern = page.getByTestId('rule-pattern');
	await expect(pattern).toBeFocused();
	await page.getByTestId('rule-tag').selectOption({ label: 'linux' });
	await page.getByTestId('rule-field').selectOption('title');
	const preview = page.getByTestId('rule-preview');

	// (?i) is Go RE2 syntax that JavaScript rejects; the daemon runs it.
	await pattern.fill('(?i)LINUX 6\\.18');
	await expect(page.getByTestId('rule-preview-summary')).toContainText('1 match in the last');
	await expect(preview.getByTestId('rule-match')).toHaveCount(1);
	await expect(preview.getByTestId('rule-match')).toContainText('Linux 6.18 released');

	// A broader pattern updates the preview as you type.
	await pattern.fill('(?i)linux');
	await expect(preview.getByTestId('rule-match')).toHaveCount(2);

	// An invalid pattern shows the daemon's error and can't be saved.
	await pattern.fill('(?i)(linux');
	await expect(page.getByTestId('rule-preview-error')).toContainText('missing closing )');
	await save(page, pattern);
	await expect(form.locator('.server-error')).toContainText('missing closing )');

	const first = `(?i)e2e-${project}-one`;
	await pattern.fill(first);
	await expect(page.getByTestId('rule-preview-summary')).toContainText('0 matches');
	await page.getByTestId('rule-priority').fill('7');
	if (isMobile(page)) await expect(pattern).toHaveCSS('font-size', '16px');
	await save(page, pattern);
	await expect(form).toHaveCount(0);
	const row = listRow(page, 'rules', first);
	await expect(row).toBeVisible();
	await expect(row).toContainText('[linux]');
	if (!isMobile(page)) await expect(row).toContainText('prio 7');

	// Editing adds the new rule and removes the old one.
	const count = await listRows(page, 'rules').count();
	await pick(row);
	await act(page, 'edit');
	await expect(pattern).toHaveValue(first);
	await expect(page.getByTestId('rule-tag')).toHaveValue(/\d+/);
	const second = `(?i)e2e-${project}-two`;
	await pattern.fill(second);
	await save(page, pattern);
	await expect(form).toHaveCount(0);
	await expect(listRow(page, 'rules', second)).toBeVisible();
	await expect(listRow(page, 'rules', first)).toHaveCount(0);
	await expect(listRows(page, 'rules')).toHaveCount(count);
	await expect(listRow(page, 'rules', second)).toContainText('[linux]');

	await act(page, 'delete');
	await expect(page.getByTestId('confirm')).toContainText('Items it already tagged keep the tag.');
	await confirmDelete(page);
	await expect(listRow(page, 'rules', second)).toHaveCount(0);
});

test.describe('command line', () => {
	test.skip(({ isMobile }) => isMobile, 'desktop only');

	test(':sources, :tags, :rules and :feed switch views and keep the feed filter', async ({ page }) => {
		await page.goto('/?sort=oldest');
		await expect(page.getByTestId('status').locator('[data-status]')).toHaveAttribute('data-status', 'connected');
		await page.keyboard.press(':');
		await expect(page.getByTestId('command')).toBeFocused();
		await page.keyboard.type('nope');
		await page.keyboard.press('Enter');
		await expect(page.getByTestId('command-line')).toContainText('unknown command: nope');
		await page.keyboard.press('Escape');
		await expect(page.getByTestId('command-line')).toHaveCount(0);

		for (const view of ['tags', 'rules', 'sources'] as const) {
			await page.keyboard.press(':');
			await page.keyboard.type(view);
			await page.keyboard.press('Enter');
			await expect(page).toHaveURL(new RegExp(`/${view}$`));
		}
		await page.keyboard.press(':');
		await page.keyboard.type('feed');
		await page.keyboard.press('Enter');
		await expect(page).toHaveURL(/\/\?sort=oldest$/);
	});

	test('j/k move the selection in a management view', async ({ page }) => {
		await page.goto('/sources');
		const rows = listRows(page, 'sources');
		await expect(rows.first()).toHaveAttribute('aria-selected', 'true');
		await page.keyboard.press('j');
		await expect(rows.nth(1)).toHaveAttribute('aria-selected', 'true');
		await page.keyboard.press('k');
		await expect(rows.first()).toHaveAttribute('aria-selected', 'true');
		// Space on the list toggles, so check the keys a panel swallows: with
		// the add form open, j is typing, not movement.
		await page.keyboard.press('a');
		await page.keyboard.type('j');
		await expect(page.getByTestId('source-name')).toHaveValue('j');
		await expect(rows.first()).toHaveAttribute('aria-selected', 'true');
		await page.keyboard.press('Escape');
		await expect(page.getByTestId('source-form')).toHaveCount(0);
	});
});

test('management mutations are refused cross-origin', async ({ page }) => {
	for (const [method, path, data] of [
		['post', '/api/sources', { name: 'x', url: 'https://x.example/feed' }],
		['patch', '/api/sources/1', { enabled: false }],
		['delete', '/api/tags/1', {}],
		['post', '/api/rules', { tag_id: '1', pattern: 'x' }],
		['delete', '/api/rules/1', {}]
	] as const) {
		const res = await page.request[method](path, { data, headers: { Origin: 'https://evil.example' } });
		expect(res.status(), `${method} ${path}`).toBe(403);
	}
	// Nothing changed.
	const sources = (await (await page.request.get('/api/sources')).json()) as { sources: { enabled?: boolean }[] };
	expect(sources.sources[0]?.enabled).toBe(true);
});
