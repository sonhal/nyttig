import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { LatestRequest, type Outcome } from './latest';

describe('LatestRequest', () => {
	beforeEach(() => vi.useFakeTimers());
	afterEach(() => vi.useRealTimers());

	function setup() {
		const calls: { q: string; signal: AbortSignal; resolve: (v: string) => void; reject: (e: unknown) => void }[] = [];
		const results: Outcome<string, string>[] = [];
		const lr = new LatestRequest<string, string>(
			(q, signal) =>
				new Promise((resolve, reject) => {
					calls.push({ q, signal, resolve, reject });
				}),
			250,
			(o) => results.push(o)
		);
		return { lr, calls, results };
	}

	it('debounces: only the last query in a burst is sent', async () => {
		const { lr, calls, results } = setup();
		lr.schedule('r');
		lr.schedule('ru');
		lr.schedule('rus');
		await vi.advanceTimersByTimeAsync(249);
		expect(calls).toHaveLength(0);
		await vi.advanceTimersByTimeAsync(1);
		expect(calls.map((c) => c.q)).toEqual(['rus']);
		calls[0]?.resolve('3 matches');
		await vi.runAllTimersAsync();
		expect(results).toEqual([{ ok: true, query: 'rus', value: '3 matches' }]);
	});

	it('aborts a request in flight and drops its late answer', async () => {
		const { lr, calls, results } = setup();
		lr.schedule('a');
		await vi.advanceTimersByTimeAsync(250);
		lr.schedule('b');
		expect(calls[0]?.signal.aborted).toBe(true);
		calls[0]?.resolve('late');
		await vi.advanceTimersByTimeAsync(250);
		calls[1]?.resolve('fresh');
		await vi.runAllTimersAsync();
		expect(results).toEqual([{ ok: true, query: 'b', value: 'fresh' }]);
	});

	it('reports errors of the latest request, not of aborted ones', async () => {
		const { lr, calls, results } = setup();
		lr.schedule('(');
		await vi.advanceTimersByTimeAsync(250);
		const err = new Error('invalid pattern');
		calls[0]?.reject(err);
		await vi.runAllTimersAsync();
		expect(results).toEqual([{ ok: false, query: '(', error: err }]);

		lr.schedule('x');
		await vi.advanceTimersByTimeAsync(250);
		lr.cancel();
		calls[1]?.reject(new DOMException('aborted', 'AbortError'));
		await vi.runAllTimersAsync();
		expect(results).toHaveLength(1);
	});

	it('cancel stops a scheduled request', async () => {
		const { lr, calls } = setup();
		lr.schedule('a');
		lr.cancel();
		await vi.runAllTimersAsync();
		expect(calls).toHaveLength(0);
	});
});
