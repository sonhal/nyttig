import { expect, test, type Page } from '@playwright/test';
import { BASE_URL, FEEDS_URL } from './feeds.mjs';

// An assessor fetches its work by saved view name: GET /api/items?view=NAME.
// The view holds the whole work definition (tag, window, "not assessed by"),
// assessing an item drops it from the list, editing the view retargets the
// assessor, and request parameters override or clear the view's fields. Both
// projects share one daemon, so every name carries the project and everything
// is removed again.

const project = () => test.info().project.name;
const headers = { Origin: BASE_URL };

interface Ids {
	assessor: string;
	tag: string;
	source: string;
	view: string;
}
let ids: Partial<Ids> = {};

const titles = async (page: Page, query: string): Promise<string[]> => {
	const res = await page.request.get(`/api/items?${query}`);
	expect(res.status(), await res.text()).toBe(200);
	const body = (await res.json()) as { items?: { id: string; title: string }[] };
	return (body.items ?? []).map((i) => i.title).sort();
};

test.afterEach(async ({ page }) => {
	const del = (path?: string) => (path ? page.request.delete(path, { headers, data: {} }) : undefined);
	await del(ids.view && `/api/views/${ids.view}`);
	await del(ids.source && `/api/sources/${ids.source}`);
	await del(ids.tag && `/api/tags/${ids.tag}`);
	await del(ids.assessor && `/api/assessors/${ids.assessor}`);
	ids = {};
});

test('assessor view: the work list shrinks as items are assessed, and the view retargets it', async ({ page }) => {
	await page.goto('/');
	const name = `avw-${project()}`;
	const post = async (path: string, data: unknown) => {
		const res = await page.request.post(path, { headers, data });
		expect(res.status(), await res.text()).toBeLessThan(300);
		return (await res.json()) as { id: string };
	};
	ids.assessor = (await post('/api/assessors', { name })).id;
	ids.tag = (await post('/api/tags', { name })).id;
	await post('/api/rules', { tag_id: ids.tag, pattern: name, field: 'title' });
	ids.source = (await post('/api/sources', { name, url: `${FEEDS_URL}/dated/${name}.xml`, refresh_sec: 3600 })).id;
	await expect.poll(() => titles(page, `source=${ids.source}`)).toHaveLength(4);

	// The work definition: this tag, the last 7 days, not assessed by this assessor.
	const created = await page.request.post('/api/views', {
		headers,
		data: { name: `work ${name}`, filter: { tag: ids.tag, since: '7d', unassessed: ids.assessor } }
	});
	expect(created.status()).toBe(201);
	ids.view = ((await created.json()) as { id: string }).id;
	const view = encodeURIComponent(`WORK ${name}`); // any case

	const hour = `${name} hour`;
	const days2 = `${name} 2 days`;
	const days10 = `${name} 10 days`;
	expect(await titles(page, `view=${view}`)).toEqual([days2, hour].sort());
	expect(await titles(page, `view=${ids.view}`)).toEqual([days2, hour].sort());

	// Assess one: it drops out of the work list.
	const list = (await (await page.request.get(`/api/items?view=${view}`)).json()) as { items: { id: string; title: string }[] };
	const first = list.items.find((i) => i.title === hour)!;
	const put = await page.request.put(`/api/items/${first.id}/assessments`, {
		headers,
		data: { assessor: ids.assessor, score: 0.8, note: 'looked at it' }
	});
	expect(put.status()).toBe(200);
	expect(await titles(page, `view=${view}`)).toEqual([days2]);

	// Editing the view widens the window; the assessor changes nothing.
	const patch = await page.request.patch(`/api/views/${ids.view}`, {
		headers,
		data: { filter: { tag: ids.tag, since: '1mo', unassessed: ids.assessor } }
	});
	expect(patch.status()).toBe(200);
	expect(await titles(page, `view=${view}`)).toEqual([days10, days2].sort());

	// Parameters override the view: no "not assessed by" lists everything in
	// the window again; an explicit cutoff beats the window.
	expect(await titles(page, `view=${view}&unassessed=`)).toEqual([days10, days2, hour].sort());
	expect(await titles(page, `view=${view}&unassessed=&after=`)).toHaveLength(4);
	const nowSec = Math.floor(Date.now() / 1000);
	expect(await titles(page, `view=${view}&unassessed=&after=${nowSec - 6 * 3600}`)).toEqual([hour]);

	// An unknown view is a 404.
	const missing = await page.request.get('/api/items?view=no-such-view-' + name);
	expect(missing.status()).toBe(404);
});
