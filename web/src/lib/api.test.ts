import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, isNotFound, removeRule, removeSource, removeTag } from './api';

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
