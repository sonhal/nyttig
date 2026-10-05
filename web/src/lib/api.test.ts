import { afterEach, describe, expect, it, vi } from 'vitest';
import {
	addDigest,
	ApiError,
	getDigest,
	isNotFound,
	listDigests,
	listDigestSeries,
	putAssessment,
	removeAssessment,
	removeAssessor,
	removeDigest,
	removeDigestSeries,
	removeRule,
	removeSource,
	removeTag,
	reorderDigestSeries,
	searchItems,
	updateDigest,
	updateDigestSeries
} from './api';
import { defaultFilter } from './filter';

function stubFetch(status: number, body?: unknown) {
	const fn = vi.fn(async () =>
		status === 204
			? new Response(null, { status })
			: new Response(JSON.stringify(body ?? {}), { status, headers: { 'Content-Type': 'application/json' } })
	);
	vi.stubGlobal('fetch', fn);
	return fn;
}

afterEach(() => vi.unstubAllGlobals());

describe('removing something that does not exist', () => {
	it.each([
		['source', removeSource],
		['tag', removeTag],
		['rule', removeRule]
	])('%s: a 404 rejects with the daemon message', async (what, remove) => {
		stubFetch(404, { error: `${what} 7 not found` });
		const err = await remove('7').catch((e: unknown) => e);
		expect(err).toBeInstanceOf(ApiError);
		expect((err as ApiError).message).toBe(`${what} 7 not found`);
		expect(isNotFound(err)).toBe(true);
	});

	it('a 204 resolves', async () => {
		stubFetch(204);
		await expect(removeSource('7')).resolves.toBeUndefined();
	});

	it('other failures are not "not found"', async () => {
		stubFetch(503, { error: 'daemon unavailable' });
		const err = await removeTag('7').catch((e: unknown) => e);
		expect(isNotFound(err)).toBe(false);
		expect(isNotFound(new Error('x'))).toBe(false);
	});
});

describe('searchItems', () => {
	const urlOf = (fn: ReturnType<typeof stubFetch>) => String((fn.mock.calls[0] as unknown[])[0]);

	it('asks for a tag and its child tags by default', async () => {
		const fn = stubFetch(200, { total: 3 });
		await expect(searchItems({ ...defaultFilter, tag: '4' }, 1)).resolves.toEqual({ items: [], total: 3 });
		const u = new URL(urlOf(fn), 'http://x');
		expect(u.searchParams.get('tag')).toBe('4');
		expect(u.searchParams.has('tag_exact')).toBe(false);
	});

	it('exact asks for the tag alone', async () => {
		const fn = stubFetch(200, {});
		await searchItems({ ...defaultFilter, tag: '4' }, 1, 0, true);
		expect(new URL(urlOf(fn), 'http://x').searchParams.get('tag_exact')).toBe('1');
	});

	it('sends the snapshot cutoff as after, never the window', async () => {
		const fn = stubFetch(200, {});
		await searchItems({ ...defaultFilter, tag: '4', since: '7d' }, 50, 100, false, 1788264000);
		const u = new URL(urlOf(fn), 'http://x');
		expect(u.searchParams.get('after')).toBe('1788264000');
		expect(u.searchParams.has('since')).toBe(false);
		expect(u.searchParams.get('tag')).toBe('4');
		expect(u.searchParams.get('limit')).toBe('50');
		expect(u.searchParams.get('offset')).toBe('100');
	});

	it('sends no cutoff without one', async () => {
		const fn = stubFetch(200, {});
		await searchItems({ ...defaultFilter, since: '7d' }, 1);
		const u = new URL(urlOf(fn), 'http://x');
		expect(u.searchParams.has('after')).toBe(false);
		expect(u.searchParams.has('since')).toBe(false);
	});

	it('exact without a tag sends nothing extra', async () => {
		const fn = stubFetch(200, {});
		await searchItems(defaultFilter, 1, 0, true);
		expect(new URL(urlOf(fn), 'http://x').searchParams.has('tag_exact')).toBe(false);
	});
});

describe('assessments', () => {
	it('putAssessment sends the body as JSON to the item', async () => {
		const fn = stubFetch(200, { id: '8' });
		await putAssessment('12', { assessor: '3', tag: '5', score: 0, note: 'n' });
		const [url, init] = fn.mock.calls[0] as unknown as [string, RequestInit];
		expect(url).toBe('/api/items/12/assessments');
		expect(init.method).toBe('PUT');
		expect(JSON.parse(init.body as string)).toEqual({ assessor: '3', tag: '5', score: 0, note: 'n' });
		expect((init.headers as Record<string, string>)['Content-Type']).toBe('application/json');
	});

	it('removeAssessment names the assessor and, optionally, the tag', async () => {
		const fn = stubFetch(204);
		await removeAssessment('12', '3');
		await removeAssessment('12', '3', '5');
		expect(fn.mock.calls.map((c) => (c as unknown as [string])[0])).toEqual([
			'/api/items/12/assessments?assessor=3',
			'/api/items/12/assessments?assessor=3&tag=5'
		]);
	});

	it('the item list sends the assessor filters', async () => {
		const fn = stubFetch(200, { items: [], total: 0 });
		await searchItems({ ...defaultFilter, assessor: '3', minScore: 0.7, unassessed: '4', sort: 'score' }, 10);
		const url = (fn.mock.calls[0] as unknown as [string])[0];
		expect(url).toBe('/api/items?sort=score&assessor=3&min_score=0.7&unassessed=4&limit=10');
	});

	it('removing an unknown assessor is a 404', async () => {
		stubFetch(404, { error: 'assessor 7 not found' });
		expect(isNotFound(await removeAssessor('7').catch((e: unknown) => e))).toBe(true);
	});
});

describe('digests', () => {
	const call = (fn: ReturnType<typeof stubFetch>) => fn.mock.calls[0] as unknown as [string, RequestInit];

	it('lists the series of every assessor, or of one', async () => {
		const fn = stubFetch(200, { series: [{ id: '1' }] });
		await expect(listDigestSeries()).resolves.toEqual([{ id: '1' }]);
		await listDigestSeries('7');
		expect(fn.mock.calls.map((c) => (c as unknown as [string])[0])).toEqual(['/api/digest-series', '/api/digest-series?assessor=7']);
		stubFetch(200, {});
		await expect(listDigestSeries()).resolves.toEqual([]);
	});

	it('pages a series\' digests with the before cursor, and asks for bodies only when told', async () => {
		const fn = stubFetch(200, { digests: [{ id: '5' }], has_more: true });
		await expect(listDigests('3')).resolves.toEqual({ digests: [{ id: '5' }], hasMore: true });
		await listDigests('3', { before: '5', limit: 20, withBody: true });
		expect(fn.mock.calls.map((c) => (c as unknown as [string])[0])).toEqual([
			'/api/digests?series=3',
			'/api/digests?series=3&before=5&limit=20&body=1'
		]);
		stubFetch(200, {});
		await expect(listDigests('3')).resolves.toEqual({ digests: [], hasMore: false });
	});

	it('gets one digest', async () => {
		const fn = stubFetch(200, { id: '9', title: 't' });
		await expect(getDigest('9')).resolves.toEqual({ id: '9', title: 't' });
		expect(call(fn)[0]).toBe('/api/digests/9');
		expect(call(fn)[1].method).toBe('GET');
	});

	it('writes with JSON bodies', async () => {
		let fn = stubFetch(201, { id: '9' });
		await addDigest({ series: '3', title: 't', body: 'b', period_start: '2026-10-05T00:00:00Z', period_end: '2026-10-05T23:59:59Z', items: ['1'], inputs: [] });
		expect(call(fn)[0]).toBe('/api/digests');
		expect(call(fn)[1].method).toBe('POST');
		expect(JSON.parse(call(fn)[1].body as string)).toMatchObject({ series: '3', items: ['1'], inputs: [] });

		fn = stubFetch(200, {});
		await updateDigest('9', { items: [] });
		expect(call(fn)[0]).toBe('/api/digests/9');
		expect(call(fn)[1].method).toBe('PATCH');
		expect(JSON.parse(call(fn)[1].body as string)).toEqual({ items: [] });

		fn = stubFetch(200, {});
		await updateDigestSeries('3', { description: '' });
		expect(call(fn)[0]).toBe('/api/digest-series/3');
		expect(JSON.parse(call(fn)[1].body as string)).toEqual({ description: '' });

		fn = stubFetch(200, { series: [{ id: '2' }, { id: '1' }] });
		await expect(reorderDigestSeries(['2', '1'])).resolves.toEqual([{ id: '2' }, { id: '1' }]);
		expect(call(fn)[0]).toBe('/api/digest-series/order');
		expect(call(fn)[1].method).toBe('PUT');
		expect(JSON.parse(call(fn)[1].body as string)).toEqual({ ids: ['2', '1'] });
	});

	it('ids are encoded in the path', async () => {
		const fn = stubFetch(204);
		await removeDigest('a/b');
		expect(call(fn)[0]).toBe('/api/digests/a%2Fb');
	});

	it.each([
		['digest', removeDigest],
		['series', removeDigestSeries]
	])('removing a %s that does not exist is a 404', async (what, remove) => {
		stubFetch(404, { error: `${what} 7 not found` });
		const err = await remove('7').catch((e: unknown) => e);
		expect(isNotFound(err)).toBe(true);
		expect((err as ApiError).message).toBe(`${what} 7 not found`);
	});
});
