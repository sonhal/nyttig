import type { Filter, Sort } from './types';

export const defaultFilter: Filter = { q: '', source: '', tag: '', sort: 'newest', unviewed: false };

const ID_RE = /^[1-9][0-9]{0,18}$/;
const MAX_QUERY = 500;

/**
 * Reads the filter from the page URL (?q=&source=&tag=&sort=&unviewed=1).
 * Anything malformed falls back to the default, so a hand-edited or stale
 * bookmark never produces a request the API rejects.
 */
export function filterFromParams(p: URLSearchParams): Filter {
	const source = p.get('source') ?? '';
	const tag = p.get('tag') ?? '';
	const sort = p.get('sort');
	const unviewed = p.get('unviewed');
	return {
		q: (p.get('q') ?? '').slice(0, MAX_QUERY),
		source: ID_RE.test(source) ? source : '',
		tag: ID_RE.test(tag) ? tag : '',
		sort: sort === 'oldest' ? 'oldest' : 'newest',
		unviewed: unviewed === '1' || unviewed === 'true'
	};
}

/**
 * Encodes a filter as query parameters, leaving out defaults. The same
 * parameters are used by the page URL, /api/stream and /api/items.
 */
export function filterToParams(f: Filter): URLSearchParams {
	const p = new URLSearchParams();
	if (f.q) p.set('q', f.q);
	if (f.source) p.set('source', f.source);
	if (f.tag) p.set('tag', f.tag);
	if (f.sort !== 'newest') p.set('sort', f.sort);
	if (f.unviewed) p.set('unviewed', '1');
	return p;
}

/** "?a=b" or "" for the default filter. */
export function filterQuery(f: Filter): string {
	const s = filterToParams(f).toString();
	return s ? '?' + s : '';
}

export function sameFilter(a: Filter, b: Filter): boolean {
	return filterQuery(a) === filterQuery(b);
}

/** The next value in a cycle of IDs where "" (all) comes first, like the TUI. */
export function cycleID(ids: string[], current: string): string {
	const all = ['', ...ids];
	const i = all.indexOf(current);
	return all[(i + 1) % all.length] ?? '';
}

export function nextSort(s: Sort): Sort {
	return s === 'newest' ? 'oldest' : 'newest';
}
