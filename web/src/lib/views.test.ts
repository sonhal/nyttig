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
		expect(viewToFilter(security)).toEqual({ q: 'openssl', source: '5', tag: '4', sort: 'oldest', unviewed: true });
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
