import { describe, expect, it } from 'vitest';
import {
	apiParams,
	cycleID,
	defaultFilter,
	filterFromParams,
	filterQuery,
	filterToParams,
	nextSort,
	parseScore,
	sameFilter,
	withAssessor
} from './filter';

describe('filter ⇄ URL', () => {
	it('round-trips', () => {
		const f = { ...defaultFilter, q: 'kernel panic', source: '3', tag: '12', sort: 'oldest' as const, unviewed: true, since: '7d' };
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

describe('assessor filters in the URL', () => {
	const f = {
		...defaultFilter,
		assessor: '3',
		minScore: 0.7,
		unassessed: '4',
		sort: 'score' as const,
		tag: '12'
	};

	it('round-trips with the same keys as the API', () => {
		expect(filterQuery(f)).toBe('?tag=12&sort=score&assessor=3&min_score=0.7&unassessed=4');
		expect(filterFromParams(filterToParams(f))).toEqual(f);
	});

	it('carries the window next to the assessor fields, and sends it as after', () => {
		const w = { ...f, since: '1d' };
		expect(filterQuery(w)).toBe('?tag=12&sort=score&since=1d&assessor=3&min_score=0.7&unassessed=4');
		expect(filterFromParams(filterToParams(w))).toEqual(w);
		const api = apiParams(w, 1700000000);
		expect(api.get('since')).toBeNull();
		expect(api.get('after')).toBe('1700000000');
		expect(api.get('assessor')).toBe('3');
		expect(api.get('unassessed')).toBe('4');
	});

	it('keeps a minimum of 0', () => {
		const zero = { ...defaultFilter, assessor: '3', minScore: 0 };
		expect(filterQuery(zero)).toBe('?assessor=3&min_score=0');
		expect(filterFromParams(filterToParams(zero))).toEqual(zero);
	});

	it('drops a minimum score or score sort without an assessor', () => {
		const p = filterFromParams(new URLSearchParams('min_score=0.5&sort=score'));
		expect(p.minScore).toBeNull();
		expect(p.sort).toBe('newest');
		expect(filterQuery({ ...defaultFilter, minScore: 0.5 })).toBe('');
		// An unassessed filter stands alone.
		expect(filterFromParams(new URLSearchParams('unassessed=4')).unassessed).toBe('4');
	});

	it('ignores malformed values', () => {
		for (const bad of ['x', '-0.1', '1.5', 'NaN', 'Infinity', '', '0.7abc', '1e-1']) {
			expect(filterFromParams(new URLSearchParams({ assessor: '3', min_score: bad })).minScore, bad).toBeNull();
		}
		expect(filterFromParams(new URLSearchParams('assessor=0&unassessed=-1'))).toEqual(defaultFilter);
		expect(parseScore('.5')).toBe(0.5);
		expect(parseScore('1')).toBe(1);
		expect(parseScore('0')).toBe(0);
		expect(parseScore(null)).toBeNull();
	});

	it('sameFilter sees the assessor fields', () => {
		expect(sameFilter(f, { ...f })).toBe(true);
		expect(sameFilter(f, { ...f, minScore: 0.8 })).toBe(false);
		expect(sameFilter(f, { ...f, unassessed: '' })).toBe(false);
		expect(sameFilter(f, { ...f, assessor: '5' })).toBe(false);
	});

	it('withAssessor clears what needs an assessor', () => {
		expect(withAssessor(f, '5')).toEqual({ ...f, assessor: '5' });
		expect(withAssessor(f, '')).toEqual({ ...f, assessor: '', minScore: null, sort: 'newest' });
		// Another sort is kept, and so is the unassessed filter.
		expect(withAssessor({ ...f, sort: 'oldest' }, '')).toMatchObject({ sort: 'oldest', unassessed: '4' });
		expect(nextSort('score')).toBe('newest');
	});
});
