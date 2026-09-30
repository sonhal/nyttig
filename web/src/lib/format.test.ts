import { describe, expect, it } from 'vitest';
import { formatDate, formatTime, shortDuration } from './format';
import { fetchTimes } from './meta';

describe('formatDate', () => {
	it('formats dd.MM HH:mm in local time', () => {
		const d = new Date(2026, 8, 3, 7, 5); // 3 Sep 2026 07:05 local
		expect(formatDate(d.toISOString())).toBe('03.09 07:05');
		expect(formatTime(d.toISOString())).toBe('07:05');
	});

	it('is empty for missing or bad timestamps', () => {
		expect(formatDate(undefined)).toBe('');
		expect(formatDate('nope')).toBe('');
	});
});

describe('shortDuration', () => {
	it.each([
		[0, '0s'],
		[-5000, '0s'],
		[45_000, '45s'],
		[12 * 60_000, '12m'],
		[3 * 3600_000, '3h'],
		[50 * 3600_000, '2d']
	])('%d ms', (ms, want) => {
		expect(shortDuration(ms)).toBe(want);
	});
});

describe('fetchTimes', () => {
	it('uses the newest last fetch and the earliest next fetch of enabled sources', () => {
		const now = Date.parse('2026-09-30T12:00:00Z');
		const t = fetchTimes(
			[
				{ id: '1', enabled: true, refresh_sec: 600, last_fetch: '2026-09-30T11:55:00Z' },
				{ id: '2', enabled: true, refresh_sec: 3600, last_fetch: '2026-09-30T11:58:00Z' },
				{ id: '3', enabled: false, refresh_sec: 60, last_fetch: '2026-09-30T11:00:00Z' }
			],
			now
		);
		expect(t.last).toBe(Date.parse('2026-09-30T11:58:00Z'));
		expect(t.next).toBe(Date.parse('2026-09-30T12:05:00Z'));
		expect(fetchTimes([], now)).toEqual({ last: null, next: null });
		// Never fetched: due now.
		expect(fetchTimes([{ id: '1', enabled: true, refresh_sec: 60 }], now).next).toBe(now);
	});
});
