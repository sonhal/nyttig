import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, isNotFound, removeRule, removeSource, removeTag, searchItems } from './api';
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
