import { afterEach, describe, expect, it, vi } from 'vitest';
import { ensureMe, parseRate } from './rate';

afterEach(() => vi.unstubAllGlobals());

function json(status: number, body: unknown) {
	return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

describe('parseRate', () => {
	it('reads a score and an optional note', () => {
		expect(parseRate('0.8')).toEqual({ ok: true, score: 0.8, note: '' });
		expect(parseRate(' .5   worth reading ')).toEqual({ ok: true, score: 0.5, note: 'worth reading' });
		expect(parseRate('1 a\tb  c')).toEqual({ ok: true, score: 1, note: 'a\tb  c' });
		expect(parseRate('0')).toEqual({ ok: true, score: 0, note: '' });
	});

	it('refuses anything else, saying what is expected', () => {
		for (const bad of ['', '  ', '1.5', '-1', 'high', '0.5x note', '1e-1', 'NaN']) {
			const r = parseRate(bad);
			expect(r.ok, bad).toBe(false);
			if (!r.ok) expect(r.error).toContain('a score from 0 to 1');
		}
		const r = parseRate('high tag');
		expect(!r.ok && r.error).toContain('not high');
	});
});

describe('ensureMe', () => {
	it('uses the assessor it is given, without a request', async () => {
		const fn = vi.fn();
		vi.stubGlobal('fetch', fn);
		expect(await ensureMe([{ id: '2', name: 'claude' }, { id: '5', name: 'me' }])).toBe('5');
		expect(fn).not.toHaveBeenCalled();
	});

	it('creates it on first use', async () => {
		const fn = vi.fn(async () => json(201, { id: '9', name: 'me' }));
		vi.stubGlobal('fetch', fn);
		expect(await ensureMe([{ id: '2', name: 'claude' }])).toBe('9');
		const [url, init] = fn.mock.calls[0] as unknown as [string, RequestInit];
		expect(url).toBe('/api/assessors');
		expect(init.method).toBe('POST');
		expect(JSON.parse(init.body as string)).toMatchObject({ name: 'me' });
	});

	it('finds it when another client created it first', async () => {
		const fn = vi.fn(async (url: string) =>
			url === '/api/assessors' && fn.mock.calls.length === 1
				? json(409, { error: 'assessor "me" already exists' })
				: json(200, { assessors: [{ id: '7', name: 'me' }] })
		);
		vi.stubGlobal('fetch', fn);
		expect(await ensureMe([])).toBe('7');
	});

	it('passes other failures on', async () => {
		vi.stubGlobal('fetch', vi.fn(async () => json(503, { error: 'daemon unavailable' })));
		await expect(ensureMe([])).rejects.toThrow('daemon unavailable');
	});
});
