// Saved views: named filters kept by the daemon. A view resolves to a plain
// Filter in the browser, so the feed, the stream and the Hub know nothing of
// them. The URL stays the source of truth for the filter
// (/?view=2&tag=3&unviewed=1); "view" only says which tab is active, and the
// tab shows "*" when the filter has been changed from what the view saved.
//
// Everything here is pure: the views come in as arguments.

import { defaultFilter, filterToParams, sameFilter } from './filter';
import type { Filter, SavedView, ViewFilter } from './types';

/** The most views the daemon keeps. */
export const MAX_VIEWS = 100;

/** How many favorites have a number key (1-9). */
export const NUMBERED_TABS = 9;

/** The filter a view stands for. */
export function viewToFilter(v: SavedView): Filter {
	const f = v.filter ?? {};
	return {
		q: f.q ?? '',
		source: f.source ?? '',
		tag: f.tag ?? '',
		sort: f.sort === 'oldest' ? 'oldest' : 'newest',
		unviewed: !!f.unviewed
	};
}

/** A filter as the API takes it: defaults left out (a PATCH replaces the whole filter). */
export function filterToViewBody(f: Filter): ViewFilter {
	const b: ViewFilter = {};
	if (f.q) b.q = f.q;
	if (f.source) b.source = f.source;
	if (f.tag) b.tag = f.tag;
	if (f.sort !== defaultFilter.sort) b.sort = f.sort;
	if (f.unviewed) b.unviewed = true;
	return b;
}

/** "?view=<id>&<filter params>", or the plain filter query when there is no view. */
export function viewSearch(id: string, f: Filter): string {
	const p = new URLSearchParams();
	if (id) p.set('view', id);
	for (const [k, v] of filterToParams(f)) p.set(k, v);
	const s = p.toString();
	return s ? '?' + s : '';
}

/** Where a view's tab goes: the feed with the view's whole filter. */
export function viewHref(v: SavedView): string {
	return '/' + viewSearch(v.id ?? '', viewToFilter(v));
}

/** The view the URL says is active, if it still exists. */
export function activeView(params: URLSearchParams, views: readonly SavedView[]): SavedView | undefined {
	const id = params.get('view');
	return id ? views.find((v) => v.id === id) : undefined;
}

/** True when the filter differs from what the view saved. */
export function isModified(view: SavedView, filter: Filter): boolean {
	return !sameFilter(viewToFilter(view), filter);
}

/** The favorite views in display order: the tabs. */
export function favorites(views: readonly SavedView[]): SavedView[] {
	return views
		.map((v, i) => ({ v, i }))
		.filter((x) => x.v.favorite)
		.sort((a, b) => (a.v.position ?? 0) - (b.v.position ?? 0) || a.i - b.i)
		.map((x) => x.v);
}

/** The favorite behind key n (1-9), if there is one. */
export function viewForKey(views: readonly SavedView[], n: number): SavedView | undefined {
	if (!Number.isInteger(n) || n < 1 || n > NUMBERED_TABS) return undefined;
	return favorites(views)[n - 1];
}

/**
 * A view by name for ":view": an exact name (any case) first, then a name
 * that starts with the text when only one does.
 */
export function findView(views: readonly SavedView[], name: string): SavedView | undefined {
	const want = name.trim().toLowerCase();
	if (want === '') return undefined;
	const exact = views.find((v) => (v.name ?? '').toLowerCase() === want);
	if (exact) return exact;
	const starts = views.filter((v) => (v.name ?? '').toLowerCase().startsWith(want));
	return starts.length === 1 ? starts[0] : undefined;
}

/** The views whose filter names this source or tag (deleting it drops that part of the filter). */
export function viewsUsing(by: { source: string } | { tag: string }, views: readonly SavedView[]): SavedView[] {
	return views.filter((v) => ('source' in by ? v.filter?.source === by.source : v.filter?.tag === by.tag));
}

/** "2 views filter on this source; they will stop filtering on it", or "" for none. */
export function usageWarning(what: 'source' | 'tag', n: number): string {
	if (n === 0) return '';
	return `${n} ${n === 1 ? 'view filters' : 'views filter'} on this ${what}; ${n === 1 ? 'it' : 'they'} will stop filtering on it.`;
}

/** The ids in the order after moving the item at index `from` by `by` places (clamped). */
export function moved(ids: readonly string[], from: number, by: number): string[] {
	const to = Math.max(0, Math.min(ids.length - 1, from + by));
	const out = [...ids];
	if (from < 0 || from >= out.length || to === from) return out;
	const [x] = out.splice(from, 1);
	out.splice(to, 0, x!);
	return out;
}
