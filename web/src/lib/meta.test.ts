import { describe, expect, it } from 'vitest';
import { fetchTimes, nextFetch, tagDisplays } from './meta';

const now = Date.parse('2026-09-30T12:00:00Z');

describe('nextFetch', () => {
	it('is the last fetch plus the refresh interval', () => {
		expect(nextFetch({ enabled: true, refresh_sec: 600, last_fetch: '2026-09-30T11:55:00Z' }, now)).toBe(
			Date.parse('2026-09-30T12:05:00Z')
		);
	});
	it('is due now for a source never fetched, and null when disabled', () => {
		expect(nextFetch({ enabled: true, refresh_sec: 600 }, now)).toBe(now);
		expect(nextFetch({ enabled: false, refresh_sec: 600, last_fetch: '2026-09-30T11:55:00Z' }, now)).toBeNull();
	});
	it('feeds the status bar the earliest enabled source', () => {
		const t = fetchTimes(
			[
				{ enabled: true, refresh_sec: 3600, last_fetch: '2026-09-30T11:30:00Z' },
				{ enabled: true, refresh_sec: 600, last_fetch: '2026-09-30T11:55:00Z' },
				{ enabled: false, refresh_sec: 60, last_fetch: '2026-09-30T11:59:00Z' }
			],
			now
		);
		expect(t.last).toBe(Date.parse('2026-09-30T11:59:00Z'));
		expect(t.next).toBe(Date.parse('2026-09-30T12:05:00Z'));
	});
});

describe('tagDisplays', () => {
	it('maps tags by ID and drops unsafe colors', () => {
		const m = tagDisplays([
			{ id: '1', name: 'rust', color: '#CE422B' },
			{ id: '2', name: 'evil', color: 'red;background:url(x)' },
			{ name: 'no id' }
		]);
		expect(m.get('1')).toEqual({ name: 'rust', color: '#CE422B' });
		expect(m.get('2')).toEqual({ name: 'evil', color: undefined });
		expect(m.size).toBe(2);
	});
});
