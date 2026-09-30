// The stream reducer: applies Server-Sent Events from /api/stream to the
// feed state. Pure functions, so they are unit-tested without a browser.
//
//  1. reset: start a snapshot buffer, keep showing the old rows (no flicker).
//  2. item before complete: add to the buffer.
//  3. complete: swap the buffer in; the cursor stays on the same item ID if
//     it is still there.
//  4. item after complete (live push): dedupe by ID, insert by sort order.
//  5. A reconnect sends reset + a new snapshot, so replays are idempotent.

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
}

export type StreamEvent = { type: 'reset' } | { type: 'item'; item: Item } | { type: 'complete' };

export function initialState(sort: Sort = 'newest'): FeedState {
	return { items: [], buffer: null, bufferIds: null, complete: false, cursor: 0, sort };
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
			// Keep the selection on the same item.
			const cursor = state.items.length > 0 && at <= state.cursor ? state.cursor + 1 : state.cursor;
			return { ...state, items, cursor };
		}

		case 'complete': {
			if (!state.buffer) return { ...state, complete: true };
			const selected = state.items[state.cursor]?.id;
			const items = state.buffer;
			let cursor = selected === undefined ? -1 : items.findIndex((it) => it.id === selected);
			if (cursor < 0) cursor = clampCursor(state.cursor, items.length);
			return { ...state, items, buffer: null, bufferIds: null, complete: true, cursor };
		}
	}
}

/** Marks the given IDs viewed. Returns the same state if nothing changed. */
export function markViewed(state: FeedState, ids: ReadonlySet<string>): FeedState {
	let changed = false;
	const items = state.items.map((it) => {
		if (!it.viewed && ids.has(it.id)) {
			changed = true;
			return { ...it, viewed: true };
		}
		return it;
	});
	return changed ? { ...state, items } : state;
}

/** Moves the cursor to index i, clamped to the list. */
export function moveCursor(state: FeedState, i: number): FeedState {
	const cursor = clampCursor(i, state.items.length);
	return cursor === state.cursor ? state : { ...state, cursor };
}
