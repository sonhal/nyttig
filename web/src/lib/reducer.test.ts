import { describe, expect, it } from 'vitest';
import { compareItems, initialState, markViewed, moveCursor, reduce, type FeedState, type StreamEvent } from './reducer';
import type { Item } from './types';

function item(id: string, published?: string, extra: Partial<Item> = {}): Item {
	return { id, published, ...extra };
}

function run(state: FeedState, events: StreamEvent[]): FeedState {
	return events.reduce(reduce, state);
}

const snapshot = (...items: Item[]): StreamEvent[] => [
	{ type: 'reset' },
	...items.map((it): StreamEvent => ({ type: 'item', item: it })),
	{ type: 'complete' }
];

const ids = (s: FeedState) => s.items.map((it) => it.id);

describe('reduce', () => {
	it('buffers the snapshot until complete, keeping the old rows visible', () => {
		let s = run(initialState(), snapshot(item('1', '2026-09-30T10:00:00Z')));
		expect(ids(s)).toEqual(['1']);

		s = reduce(s, { type: 'reset' });
		s = reduce(s, { type: 'item', item: item('2', '2026-09-30T11:00:00Z') });
		// Not swapped in yet: no flicker while the snapshot streams in.
		expect(ids(s)).toEqual(['1']);
		expect(s.complete).toBe(true);

		s = reduce(s, { type: 'complete' });
		expect(ids(s)).toEqual(['2']);
		expect(s.buffer).toBeNull();
	});

	it('keeps the snapshot order from the server', () => {
		const s = run(initialState(), snapshot(item('3', '2026-09-30T12:00:00Z'), item('1', '2026-09-30T10:00:00Z')));
		expect(ids(s)).toEqual(['3', '1']);
		expect(s.complete).toBe(true);
	});

	it('dedupes by id inside a snapshot and across reconnects', () => {
		const a = item('1', '2026-09-30T10:00:00Z');
		const b = item('2', '2026-09-30T09:00:00Z');
		let s = run(initialState(), [{ type: 'reset' }, { type: 'item', item: a }, { type: 'item', item: a }, { type: 'item', item: b }, { type: 'complete' }]);
		expect(ids(s)).toEqual(['1', '2']);
		// A reconnect replays the same snapshot: idempotent.
		s = run(s, snapshot(a, b));
		expect(ids(s)).toEqual(['1', '2']);
	});

	it('inserts live pushes by sort order and dedupes them', () => {
		let s = run(initialState('newest'), snapshot(item('2', '2026-09-30T10:00:00Z'), item('1', '2026-09-30T09:00:00Z')));
		s = reduce(s, { type: 'item', item: item('3', '2026-09-30T11:00:00Z') });
		expect(ids(s)).toEqual(['3', '2', '1']);
		// Out of order push lands in the middle.
		s = reduce(s, { type: 'item', item: item('4', '2026-09-30T09:30:00Z') });
		expect(ids(s)).toEqual(['3', '2', '4', '1']);
		// A duplicate replaces in place.
		s = reduce(s, { type: 'item', item: item('2', '2026-09-30T10:00:00Z', { title: 'updated' }) });
		expect(ids(s)).toEqual(['3', '2', '4', '1']);
		expect(s.items[1]?.title).toBe('updated');
	});

	it('appends live pushes under oldest-first sort', () => {
		let s = run(initialState('oldest'), snapshot(item('1', '2026-09-30T09:00:00Z'), item('2', '2026-09-30T10:00:00Z')));
		s = reduce(s, { type: 'item', item: item('3', '2026-09-30T11:00:00Z') });
		expect(ids(s)).toEqual(['1', '2', '3']);
	});

	it('sorts items without a published time last', () => {
		let s = run(initialState('newest'), snapshot(item('1', '2026-09-30T09:00:00Z'), item('2')));
		s = reduce(s, { type: 'item', item: item('3', '2026-09-30T08:00:00Z') });
		expect(ids(s)).toEqual(['1', '3', '2']);
	});

	it('keeps the cursor on the same item when a push lands above it', () => {
		let s = run(initialState(), snapshot(item('2', '2026-09-30T10:00:00Z'), item('1', '2026-09-30T09:00:00Z')));
		s = moveCursor(s, 1);
		expect(s.items[s.cursor]?.id).toBe('1');
		s = reduce(s, { type: 'item', item: item('3', '2026-09-30T11:00:00Z') });
		expect(s.items[s.cursor]?.id).toBe('1');
		// Also at the top: the selection stays on the item, not the index.
		s = moveCursor(s, 0);
		s = reduce(s, { type: 'item', item: item('4', '2026-09-30T12:00:00Z') });
		expect(s.items[s.cursor]?.id).toBe('3');
		// A push below the cursor doesn't move it.
		s = reduce(s, { type: 'item', item: item('5', '2026-09-30T08:00:00Z') });
		expect(s.items[s.cursor]?.id).toBe('3');
	});

	it('keeps the cursor on the same item across a new snapshot', () => {
		let s = run(initialState(), snapshot(item('a', '2026-09-30T12:00:00Z'), item('b', '2026-09-30T11:00:00Z'), item('c', '2026-09-30T10:00:00Z')));
		s = moveCursor(s, 1);
		s = run(s, snapshot(item('z', '2026-09-30T13:00:00Z'), item('a', '2026-09-30T12:00:00Z'), item('b', '2026-09-30T11:00:00Z')));
		expect(s.items[s.cursor]?.id).toBe('b');
		expect(s.cursor).toBe(2);
	});

	it('clamps the cursor when its item is gone', () => {
		let s = run(initialState(), snapshot(item('a'), item('b'), item('c')));
		s = moveCursor(s, 2);
		s = run(s, snapshot(item('x')));
		expect(s.cursor).toBe(0);
		s = run(s, snapshot());
		expect(s.cursor).toBe(0);
		expect(s.items).toEqual([]);
	});

	it('returns the same state for buffered items, so nothing re-renders', () => {
		let s = reduce(initialState(), { type: 'reset' });
		const next = reduce(s, { type: 'item', item: item('1') });
		expect(next).toBe(s);
		s = reduce(next, { type: 'complete' });
		expect(ids(s)).toEqual(['1']);
	});
});

describe('markViewed and moveCursor', () => {
	it('marks only listed, unviewed items', () => {
		const s = run(initialState(), snapshot(item('1'), item('2'), item('3', undefined, { viewed: true })));
		const next = markViewed(s, new Set(['1', '3', 'x']));
		expect(next.items.map((it) => !!it.viewed)).toEqual([true, false, true]);
		expect(markViewed(next, new Set(['1']))).toBe(next);
	});

	it('clamps the cursor', () => {
		const s = run(initialState(), snapshot(item('1'), item('2')));
		expect(moveCursor(s, 10).cursor).toBe(1);
		expect(moveCursor(s, -5).cursor).toBe(0);
		expect(moveCursor(s, 0)).toBe(s);
	});
});

describe('compareItems', () => {
	it('breaks published ties by fetch time', () => {
		const a = item('a', '2026-09-30T10:00:00Z', { fetched_at: '2026-09-30T10:05:00Z' });
		const b = item('b', '2026-09-30T10:00:00Z', { fetched_at: '2026-09-30T10:01:00Z' });
		expect(compareItems(a, b, 'newest')).toBeLessThan(0);
		expect(compareItems(a, b, 'oldest')).toBeGreaterThan(0);
	});
});
