import { describe, expect, it } from 'vitest';
import { cycleID, defaultFilter, filterFromParams, filterQuery, filterToParams, nextSort } from './filter';

describe('filter ⇄ URL', () => {
	it('round-trips', () => {
		const f = { q: 'kernel panic', source: '3', tag: '12', sort: 'oldest' as const, unviewed: true };
		expect(filterFromParams(filterToParams(f))).toEqual(f);
		expect(filterQuery(f)).toBe('?q=kernel+panic&source=3&tag=12&sort=oldest&unviewed=1');
	});

	it('leaves defaults out', () => {
		expect(filterQuery(defaultFilter)).toBe('');
		expect(filterFromParams(new URLSearchParams())).toEqual(defaultFilter);
	});

	it('ignores malformed values', () => {
		const f = filterFromParams(new URLSearchParams('source=abc&tag=-1&sort=sideways&unviewed=yes&q=' + 'x'.repeat(600)));
		expect(f.source).toBe('');
		expect(f.tag).toBe('');
		expect(f.sort).toBe('newest');
		expect(f.unviewed).toBe(false);
		expect(f.q.length).toBe(500);
		expect(filterFromParams(new URLSearchParams('source=0')).source).toBe('');
	});
});

describe('cycling', () => {
	it('cycles through all, then each id, then back to all', () => {
		const ids = ['1', '5', '9'];
		expect(cycleID(ids, '')).toBe('1');
		expect(cycleID(ids, '5')).toBe('9');
		expect(cycleID(ids, '9')).toBe('');
		expect(cycleID(ids, 'gone')).toBe('');
		expect(cycleID([], '')).toBe('');
	});

	it('toggles sort', () => {
		expect(nextSort('newest')).toBe('oldest');
		expect(nextSort('oldest')).toBe('newest');
	});
});
