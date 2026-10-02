import { describe, expect, it } from 'vitest';
import { defaultFilter } from './filter';
import type { SavedView } from './types';
import {
	activeView,
	favorites,
	filterToViewBody,
	findView,
	isModified,
	moved,
	usageWarning,
	viewForKey,
	viewHref,
	viewSearch,
	viewsUsing,
	viewToFilter
} from './views';

const security: SavedView = {
	id: '1',
	name: 'Security',
	filter: { q: 'openssl', source: '5', tag: '4', sort: 'oldest', unviewed: true },
	favorite: true,
	position: 1
};
const linux: SavedView = { id: '2', name: 'Linux', filter: { tag: '7' }, favorite: true, position: 0 };
const misc: SavedView = { id: '3', name: 'Misc', filter: {}, position: 2 };
const nofilter: SavedView = { id: '4', name: 'Everything', position: 3, favorite: true };
const views = [security, linux, misc, nofilter];

describe('viewToFilter / filterToViewBody', () => {
	it('fills in the defaults the API leaves out', () => {
		expect(viewToFilter(security)).toEqual({ ...defaultFilter, q: 'openssl', source: '5', tag: '4', sort: 'oldest', unviewed: true });
		expect(viewToFilter(linux)).toEqual({ ...defaultFilter, tag: '7' });
		expect(viewToFilter(misc)).toEqual(defaultFilter);
		expect(viewToFilter(nofilter)).toEqual(defaultFilter);
		expect(viewToFilter({ filter: { sort: 'sideways' } }).sort).toBe('newest');
	});

	it('leaves defaults out of a body', () => {
		expect(filterToViewBody(defaultFilter)).toEqual({});
		expect(filterToViewBody(viewToFilter(security))).toEqual(security.filter);
		expect(filterToViewBody({ ...defaultFilter, sort: 'oldest' })).toEqual({ sort: 'oldest' });
	});

	it('carries the window as the user typed it', () => {
		const week: SavedView = { id: '5', name: 'Week', filter: { tag: '4', since: '30d' } };
		expect(viewToFilter(week)).toEqual({ ...defaultFilter, tag: '4', since: '30d' });
		expect(filterToViewBody(viewToFilter(week))).toEqual({ tag: '4', since: '30d' });
		expect(filterToViewBody(defaultFilter)).toEqual({});
		// A window the API should never send is treated as none rather than trusted.
		expect(viewToFilter({ filter: { since: '1m' } }).since).toBe('');
		expect(viewToFilter({ filter: { since: '7D' } }).since).toBe('');
		expect(viewHref(week)).toBe('/?view=5&tag=4&since=30d');
		expect(isModified(week, viewToFilter(week))).toBe(false);
		expect(isModified(week, { ...viewToFilter(week), since: '7d' })).toBe(true);
		expect(isModified(week, { ...viewToFilter(week), since: '' })).toBe(true);
		expect(isModified(misc, { ...defaultFilter, since: '24h' })).toBe(true);
	});

	it('round-trips', () => {
		for (const v of views) expect(viewToFilter({ filter: filterToViewBody(viewToFilter(v)) })).toEqual(viewToFilter(v));
	});
});

describe('hrefs and the active view', () => {
	it('puts the view and its whole filter in the URL', () => {
		expect(viewHref(security)).toBe('/?view=1&q=openssl&source=5&tag=4&sort=oldest&unviewed=1');
		expect(viewHref(misc)).toBe('/?view=3');
		expect(viewSearch('', defaultFilter)).toBe('');
		expect(viewSearch('', { ...defaultFilter, tag: '3' })).toBe('?tag=3');
		expect(viewSearch('2', { ...defaultFilter, q: 'a b' })).toBe('?view=2&q=a+b');
	});

	it('finds the active view', () => {
		expect(activeView(new URLSearchParams('view=2&tag=7'), views)).toBe(linux);
		expect(activeView(new URLSearchParams('tag=7'), views)).toBeUndefined();
		// A deleted view: no tab is active.
		expect(activeView(new URLSearchParams('view=99'), views)).toBeUndefined();
	});

	it('knows when the filter was changed', () => {
		expect(isModified(security, viewToFilter(security))).toBe(false);
		expect(isModified(security, { ...viewToFilter(security), unviewed: false })).toBe(true);
		expect(isModified(misc, defaultFilter)).toBe(false);
		expect(isModified(misc, { ...defaultFilter, q: 'x' })).toBe(true);
	});
});

describe('favorites', () => {
	it('lists the favorites by position', () => {
		expect(favorites(views).map((v) => v.name)).toEqual(['Linux', 'Security', 'Everything']);
		expect(favorites([])).toEqual([]);
	});

	it('maps keys 1-9 to the favorites', () => {
		expect(viewForKey(views, 1)).toBe(linux);
		expect(viewForKey(views, 2)).toBe(security);
		expect(viewForKey(views, 3)).toBe(nofilter);
		expect(viewForKey(views, 4)).toBeUndefined();
		expect(viewForKey(views, 0)).toBeUndefined();
		expect(viewForKey(views, 10)).toBeUndefined();
	});

	it('numbers only the first nine', () => {
		const many = Array.from({ length: 12 }, (_, i): SavedView => ({ id: String(i + 1), name: 'v' + i, favorite: true, position: i }));
		expect(viewForKey(many, 9)?.name).toBe('v8');
		expect(favorites(many)).toHaveLength(12);
	});
});

describe('findView', () => {
	it('matches the name, then a unique prefix', () => {
		expect(findView(views, 'security')).toBe(security);
		expect(findView(views, 'sec')).toBe(security);
		expect(findView(views, 'LIN')).toBe(linux);
		expect(findView(views, 'zzz')).toBeUndefined();
		expect(findView(views, '')).toBeUndefined();
		// Ambiguous prefixes find nothing; an exact name still wins.
		const two = [{ id: '1', name: 'Rust' }, { id: '2', name: 'Rustic' }];
		expect(findView(two, 'rus')).toBeUndefined();
		expect(findView(two, 'rust')).toBe(two[0]);
	});
});

describe('viewsUsing', () => {
	it('finds the views that filter on a source or tag', () => {
		expect(viewsUsing({ source: '5' }, views)).toEqual([security]);
		expect(viewsUsing({ tag: '7' }, views)).toEqual([linux]);
		expect(viewsUsing({ tag: '5' }, views)).toEqual([]);
		expect(viewsUsing({ source: '' }, views)).toEqual([]);
	});

	it('words the warning', () => {
		expect(usageWarning('tag', 0)).toBe('');
		expect(usageWarning('tag', 1)).toBe('1 view filters on this tag; it will stop filtering on it.');
		expect(usageWarning('source', 2)).toBe('2 views filter on this source; they will stop filtering on it.');
	});
});

describe('moved', () => {
	it('moves one id, clamped to the ends', () => {
		expect(moved(['a', 'b', 'c'], 1, 1)).toEqual(['a', 'c', 'b']);
		expect(moved(['a', 'b', 'c'], 1, -1)).toEqual(['b', 'a', 'c']);
		expect(moved(['a', 'b', 'c'], 0, -1)).toEqual(['a', 'b', 'c']);
		expect(moved(['a', 'b', 'c'], 2, 5)).toEqual(['a', 'b', 'c']);
		expect(moved(['a', 'b', 'c'], 0, 9)).toEqual(['b', 'c', 'a']);
		expect(moved([], 0, 1)).toEqual([]);
	});
});

describe('assessor fields of a view', () => {
	const important: SavedView = {
		id: '9',
		name: 'Important',
		filter: { tag: '4', sort: 'score', assessor: '3', min_score: 0.7, unassessed: '5' }
	};

	it('reads them, and keeps the sort', () => {
		expect(viewToFilter(important)).toEqual({
			...defaultFilter,
			tag: '4',
			sort: 'score',
			assessor: '3',
			minScore: 0.7,
			unassessed: '5'
		});
	});

	it('keeps the window next to the assessor fields', () => {
		const work: SavedView = { id: '12', name: 'claude-cve', filter: { tag: '4', since: '1d', unassessed: '5' } };
		const f = viewToFilter(work);
		expect(f).toEqual({ ...defaultFilter, tag: '4', since: '1d', unassessed: '5' });
		expect(filterToViewBody(f)).toEqual({ tag: '4', since: '1d', unassessed: '5' });
	});

	it('leaves a score sort and a minimum without an assessor out', () => {
		expect(viewToFilter({ filter: { sort: 'score' } }).sort).toBe('newest');
		expect(viewToFilter({ filter: { min_score: 0.5 } }).minScore).toBeNull();
		expect(viewToFilter({ filter: { unassessed: '5' } }).unassessed).toBe('5');
	});

	it('writes them with the API keys, defaults left out, a minimum of 0 kept', () => {
		expect(filterToViewBody(viewToFilter(important))).toEqual(important.filter);
		expect(filterToViewBody({ ...defaultFilter, assessor: '3', minScore: 0 })).toEqual({ assessor: '3', min_score: 0 });
		expect(filterToViewBody({ ...defaultFilter, minScore: 0.5 })).toEqual({});
	});

	it('round-trips, and the URL carries the order', () => {
		expect(viewToFilter({ filter: filterToViewBody(viewToFilter(important)) })).toEqual(viewToFilter(important));
		expect(viewHref(important)).toBe('/?view=9&tag=4&sort=score&assessor=3&min_score=0.7&unassessed=5');
	});

	it('a changed sort or minimum marks the view modified', () => {
		const f = viewToFilter(important);
		expect(isModified(important, f)).toBe(false);
		expect(isModified(important, { ...f, sort: 'newest' })).toBe(true);
		expect(isModified(important, { ...f, minScore: 0.9 })).toBe(true);
	});

	it('counts the views that use an assessor, by score or by unassessed', () => {
		const other: SavedView = { id: '10', name: 'Work', filter: { unassessed: '3' } };
		const plain: SavedView = { id: '11', name: 'Plain', filter: { assessor: '8' } };
		expect(viewsUsing({ assessor: '3' }, [important, other, plain]).map((v) => v.id)).toEqual(['9', '10']);
		expect(usageWarning('assessor', 2)).toBe('2 views filter on this assessor; they will stop filtering on it.');
		expect(usageWarning('assessor', 1)).toBe('1 view filters on this assessor; it will stop filtering on it.');
	});
});
