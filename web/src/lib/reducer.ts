// The stream reducer: applies Server-Sent Events from /api/stream to the
// feed state. Pure functions, so they are unit-tested without a browser.
//
//  1. reset: start a snapshot buffer, keep showing the old rows (no flicker).
//  2. item before complete: add to the buffer.
//  3. complete: swap the buffer in; the cursor stays on the same item ID if
//     it is still there.
//  4. item after complete (live push): dedupe by ID, insert by sort order.
//  5. A reconnect sends reset + a new snapshot, so replays are idempotent.
//
// Load older (see appendOlder) and follow mode live in the same state:
//
//  - items is the snapshot (the daemon's newest 200) plus older pages
//    fetched with GET /api/items, plus live pushes, all in sort order.
//  - A reset replaces all of it, older pages included. A reset happens on a
//    filter change, a reconnect (daemon restart, overflow) and the first
//    connect. Keeping the pages would mean guessing which of them still
//    belong to the new snapshot, and reconnects are rare; the cursor stays
//    on its item if the new snapshot has it. Requests in flight are dropped
//    by epoch: every snapshot starts a new one.
//  - Paging uses the API's offset, so the reducer tracks how many database
//    rows the list covers from the top (ranked): the snapshot, each older
//    page, and every live push that sorts inside that range (it moves the
//    next unloaded row down by one). A push that sorts after the loaded
//    range is kept but not counted: overestimating would skip a row, while
//    underestimating only fetches one row twice, which dedupe drops. With
//    an unviewed-only filter, rows marked viewed leave the database result
//    and are taken off ranked too.
//  - Follow: while the view is at the top of a newest-first list the page
//    sets follow, a push at the top keeps the cursor on the newest item,
//    and nothing is counted. Otherwise pushes are counted in pending
//    ("↑ N new") and the view stays where it is.

import type { Item, Sort } from './types';

export interface FeedState {
	items: Item[];
	/** Snapshot being received, or null between complete and the next reset. */
	buffer: Item[] | null;
	/** IDs in buffer, for dedupe while buffering. */
	bufferIds: Set<string> | null;
	/** True once a snapshot has been swapped in. */
	complete: boolean;
	/** Index of the selected row. */
	cursor: number;
	sort: Sort;
	/** True while the view is at the top of a newest-first list (follow mode). */
	follow: boolean;
	/** Items pushed while not following, for "↑ N new". */
	pending: number;
	/** How many database rows items covers from the top; the next page's offset. */
	ranked: number;
	/** No older items are left to load. */
	end: boolean;
	/** An older page is being fetched. */
	loading: boolean;
	/** Starts again with every snapshot; answers from an earlier one are dropped. */
	epoch: number;
	/** The filter is unviewed-only, so marking items viewed shrinks ranked. */
	unviewedOnly: boolean;
}

export type StreamEvent = { type: 'reset' } | { type: 'item'; item: Item } | { type: 'complete' };

/** Rows per older page. The API allows 500. */
export const PAGE_SIZE = 100;

/**
 * Rows in the stream's snapshot: the daemon's default limit, which nyttig-api
 * does not override. A shorter snapshot has every matching row, so there is
 * nothing older to load (and no request to find that out). If the daemon's
 * limit ever became smaller than this, lists would end early.
 */
export const SNAPSHOT_LIMIT = 200;

export function initialState(sort: Sort = 'newest'): FeedState {
	return {
		items: [],
		buffer: null,
		bufferIds: null,
		complete: false,
		cursor: 0,
		sort,
		follow: true,
		pending: 0,
		ranked: 0,
		end: false,
		loading: false,
		epoch: 0,
		unviewedOnly: false
	};
}

function time(ts: string | undefined): number | null {
	if (!ts) return null;
	const t = Date.parse(ts);
	return Number.isNaN(t) ? null : t;
}

/**
 * Orders items like the daemon: by published time (missing last), then by
 * fetch time. Negative when a sorts before b.
 */
export function compareItems(a: Item, b: Item, sort: Sort): number {
	const dir = sort === 'newest' ? -1 : 1;
	const pa = time(a.published);
	const pb = time(b.published);
	if (pa !== pb) {
		if (pa === null) return 1;
		if (pb === null) return -1;
		return dir * (pa - pb);
	}
	const fa = time(a.fetched_at) ?? 0;
	const fb = time(b.fetched_at) ?? 0;
	return dir * (fa - fb);
}

/** Index at which item belongs in sorted items (after equal items). */
function insertionIndex(items: Item[], item: Item, sort: Sort): number {
	// Live pushes are almost always newer than everything shown, so look
	// from the end the new item most likely belongs at.
	if (sort === 'newest') {
		let i = 0;
		while (i < items.length && compareItems(items[i]!, item, sort) <= 0) i++;
		return i;
	}
	let i = items.length;
	while (i > 0 && compareItems(items[i - 1]!, item, sort) > 0) i--;
	return i;
}

function clampCursor(cursor: number, len: number): number {
	return Math.max(0, Math.min(cursor, len - 1));
}

export function reduce(state: FeedState, ev: StreamEvent): FeedState {
	switch (ev.type) {
		case 'reset':
			return { ...state, buffer: [], bufferIds: new Set() };

		case 'item': {
			const { item } = ev;
			if (state.buffer && state.bufferIds) {
				if (state.bufferIds.has(item.id)) return state;
				state.bufferIds.add(item.id);
				state.buffer.push(item);
				return state;
			}
			const existing = state.items.findIndex((it) => it.id === item.id);
			if (existing >= 0) {
				const items = state.items.slice();
				items[existing] = item;
				return { ...state, items };
			}
			const at = insertionIndex(state.items, item, state.sort);
			const items = state.items.slice();
			items.splice(at, 0, item);
			const newest = state.sort === 'newest';
			// Following, the cursor on the newest item stays on the newest.
			// Otherwise keep the selection on the same item.
			const stick = state.follow && newest && at === 0 && state.cursor === 0;
			const cursor = state.items.length > 0 && at <= state.cursor && !stick ? state.cursor + 1 : state.cursor;
			return {
				...state,
				items,
				cursor,
				ranked: at < state.ranked ? state.ranked + 1 : state.ranked,
				pending: newest && !state.follow ? state.pending + 1 : state.pending
			};
		}

		case 'complete': {
			if (!state.buffer) return { ...state, complete: true };
			const selected = state.items[state.cursor]?.id;
			const items = state.buffer;
			let cursor = selected === undefined ? -1 : items.findIndex((it) => it.id === selected);
			if (cursor < 0) cursor = clampCursor(state.cursor, items.length);
			return {
				...state,
				items,
				buffer: null,
				bufferIds: null,
				complete: true,
				cursor,
				pending: 0,
				ranked: items.length,
				end: items.length < SNAPSHOT_LIMIT,
				loading: false,
				epoch: state.epoch + 1
			};
		}
	}
}

/** Marks the given IDs viewed. Returns the same state if nothing changed. */
export function markViewed(state: FeedState, ids: ReadonlySet<string>): FeedState {
	let flipped = 0;
	const items = state.items.map((it, i) => {
		if (!it.viewed && ids.has(it.id)) {
			if (i < state.ranked) flipped++;
			return { ...it, viewed: true };
		}
		return it;
	});
	if (!items.some((it, i) => it !== state.items[i])) return state;
	// Viewed rows are gone from an unviewed-only result: the rows below them moved up.
	const ranked = state.unviewedOnly ? Math.max(0, state.ranked - flipped) : state.ranked;
	return { ...state, items, ranked };
}

/** Moves the cursor to index i, clamped to the list. */
export function moveCursor(state: FeedState, i: number): FeedState {
	const cursor = clampCursor(i, state.items.length);
	return cursor === state.cursor ? state : { ...state, cursor };
}

/** Sets follow mode (at the top of the list); being back at the top clears the count. */
export function setFollow(state: FeedState, follow: boolean): FeedState {
	if (follow === state.follow) return state;
	return { ...state, follow, pending: follow ? 0 : state.pending };
}

/** Marks an older page as being fetched. */
export function beginOlder(state: FeedState): FeedState {
	return state.loading ? state : { ...state, loading: true };
}

/** An older page failed to load; the same request can be tried again. */
export function failOlder(state: FeedState, epoch: number): FeedState {
	return epoch !== state.epoch || !state.loading ? state : { ...state, loading: false };
}

/** Whether another page should be fetched: there is a snapshot, more rows, and no request running. */
export function canLoadOlder(state: FeedState): boolean {
	return state.complete && !state.buffer && !state.loading && !state.end && state.items.length > 0;
}

/**
 * Rows to re-read below the ranked ones with an unviewed-only filter. Marking
 * rows viewed moves the rows below them up, and the browser learns of it a
 * request later than the daemon does, so an offset from ranked alone could
 * skip rows. Asking a little early costs some duplicates, which dedupe drops.
 */
const UNVIEWED_OVERLAP = 50;

/** The offset and limit of the next older page. */
export function olderRequest(state: FeedState): { offset: number; limit: number } {
	const overlap = state.unviewedOnly ? Math.min(UNVIEWED_OVERLAP, state.ranked) : 0;
	return { offset: state.ranked - overlap, limit: PAGE_SIZE + overlap };
}

/**
 * Merges an older page into the list: items already there are dropped (a
 * live push that sorted after the loaded range shows up again here), the
 * rest are merged in sort order after the ranked rows. epoch and offset are
 * the state's epoch and the request's offset when the page was requested; a
 * page from before a newer snapshot is ignored. total is the API's count of
 * matching rows.
 *
 * ranked becomes offset + page length, not ranked + page length: pushes that
 * arrived while the request was out may or may not be in the page, and
 * counting too few rows is the safe direction (see the top of this file).
 */
export function appendOlder(
	state: FeedState,
	epoch: number,
	offset: number,
	limit: number,
	page: Item[],
	total: number
): FeedState {
	if (epoch !== state.epoch || state.buffer || !state.loading) return state;
	const have = new Set(state.items.map((it) => it.id));
	const fresh = page.filter((it) => !have.has(it.id) && have.add(it.id));
	const head = state.items.slice(0, state.ranked);
	const tail = state.items.slice(state.ranked);

	// Both are sorted; on equal keys the page's rows (database order) go first.
	const merged: Item[] = [];
	let i = 0;
	let j = 0;
	while (i < fresh.length || j < tail.length) {
		const f = fresh[i];
		const t = tail[j];
		if (f && (!t || compareItems(f, t, state.sort) <= 0)) {
			merged.push(f);
			i++;
		} else if (t) {
			merged.push(t);
			j++;
		}
	}

	const items = head.concat(merged);
	return {
		...state,
		items,
		ranked: Math.min(offset + page.length, items.length),
		loading: false,
		end: page.length < limit || offset + page.length >= total
	};
}
