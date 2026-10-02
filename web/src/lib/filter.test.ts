import { describe, expect, it } from 'vitest';
import { apiParams, cycleID, defaultFilter, filterFromParams, filterQuery, filterToParams, nextSort, sameFilter } from './filter';

describe('filter ⇄ URL', () => {
	it('round-trips', () => {
		const f = { q: 'kernel panic', source: '3', tag: '12', sort: 'oldest' as const, unviewed: true, since: '7d' };
		expect(filterFromParams(filterToParams(f))).toEqual(f);
		expect(filterQuery(f)).toBe('?q=kernel+panic&source=3&tag=12&sort=oldest&unviewed=1&since=7d');
	});

	it('leaves defaults out', () => {
		expect(filterQuery(defaultFilter)).toBe('');
		expect(filterFromParams(new URLSearchParams())).toEqual(defaultFilter);
	});

	it('ignores malformed values', () => {
		const f = filterFromParams(new URLSearchParams('source=abc&tag=-1&sort=sideways&unviewed=yes&since=7D&q=' + 'x'.repeat(600)));
		expect(f.source).toBe('');
		expect(f.tag).toBe('');
		expect(f.sort).toBe('newest');
		expect(f.unviewed).toBe(false);
		expect(f.since).toBe('');
		expect(f.q.length).toBe(500);
		expect(filterFromParams(new URLSearchParams('source=0')).source).toBe('');
	});
});

describe('since', () => {
	it('is read from the URL when valid and dropped otherwise', () => {
		for (const ok of ['24h', '7d', '2w', '1mo', '1y', '9999d']) {
			expect(filterFromParams(new URLSearchParams('since=' + ok)).since).toBe(ok);
		}
		for (const bad of ['', '0d', '1m', '7', 'd', '7days', '10000d', '-1d', '7D']) {
			expect(filterFromParams(new URLSearchParams('since=' + encodeURIComponent(bad))).since).toBe('');
		}
	});

	it('makes a filter different from the same filter without it', () => {
		expect(sameFilter({ ...defaultFilter, since: '7d' }, defaultFilter)).toBe(false);
		expect(sameFilter({ ...defaultFilter, since: '7d' }, { ...defaultFilter, since: '7d' })).toBe(true);
		expect(sameFilter({ ...defaultFilter, since: '7d' }, { ...defaultFilter, since: '30d' })).toBe(false);
	});
});

describe('apiParams', () => {
	const f = { ...defaultFilter, q: 'x', tag: '4', since: '7d' };

	it('sends the cutoff as after and never the window', () => {
		expect(apiParams(f, 1788264000).toString()).toBe('q=x&tag=4&after=1788264000');
		expect(apiParams(f).toString()).toBe('q=x&tag=4');
		expect(apiParams(f, 0).toString()).toBe('q=x&tag=4&after=0');
	});

	it('is the page URL without a window', () => {
		expect(apiParams(defaultFilter).toString()).toBe('');
		expect(apiParams({ ...defaultFilter, sort: 'oldest', unviewed: true }, 5).toString()).toBe('sort=oldest&unviewed=1&after=5');
	});

	it('does not change the filter', () => {
		apiParams(f, 1);
		expect(filterQuery(f)).toBe('?q=x&tag=4&since=7d');
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
