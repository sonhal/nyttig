import { describe, expect, it } from 'vitest';
import {
	appendOlder,
	beginOlder,
	canLoadOlder,
	compareItems,
	failOlder,
	initialState,
	markViewed,
	moveCursor,
	olderRequest,
	PAGE_SIZE,
	reduce,
	setFollow,
	type FeedState,
	type StreamEvent
} from './reducer';
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
		// Not following (scrolled away): the selection stays on the item, not the index.
		let s = setFollow(
			run(initialState(), snapshot(item('2', '2026-09-30T10:00:00Z'), item('1', '2026-09-30T09:00:00Z'))),
			false
		);
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

// ── Follow mode ───────────────────────────────────────────────

describe('follow mode', () => {
	const base = () => run(initialState(), snapshot(item('3', '2026-09-30T12:00:00Z'), item('2', '2026-09-30T11:00:00Z')));
	const push = (s: FeedState, id: string, at: string) => reduce(s, { type: 'item', item: item(id, at) });

	it('starts following, and sticks the cursor to the newest item', () => {
		let s = base();
		expect(s.follow).toBe(true);
		s = push(s, '4', '2026-09-30T13:00:00Z');
		expect(ids(s)).toEqual(['4', '3', '2']);
		// The cursor was on the newest item and stays on the newest.
		expect(s.cursor).toBe(0);
		expect(s.pending).toBe(0);
	});

	it('follows only a cursor on the newest item; elsewhere it keeps its item', () => {
		let s = moveCursor(base(), 1);
		s = push(s, '4', '2026-09-30T13:00:00Z');
		expect(ids(s)[s.cursor]).toBe('2');
		expect(s.pending).toBe(0);
	});

	it('counts pushes while not following, and keeps the cursor on its item', () => {
		let s = setFollow(base(), false);
		s = push(s, '4', '2026-09-30T13:00:00Z');
		s = push(s, '5', '2026-09-30T14:00:00Z');
		expect(ids(s)).toEqual(['5', '4', '3', '2']);
		expect(s.pending).toBe(2);
		// Cursor was on '3' (index 0); it moved down with its item.
		expect(ids(s)[s.cursor]).toBe('3');
		// An update of an item already there is not new.
		s = reduce(s, { type: 'item', item: item('4', '2026-09-30T13:00:00Z', { title: 'edited' }) });
		expect(s.pending).toBe(2);
	});

	it('clears the count when following again', () => {
		let s = setFollow(base(), false);
		s = push(s, '4', '2026-09-30T13:00:00Z');
		expect(s.pending).toBe(1);
		s = setFollow(s, true);
		expect(s.pending).toBe(0);
		expect(setFollow(s, true)).toBe(s);
	});

	it('does not count in oldest-first order, where new items arrive at the end', () => {
		let s = run(initialState('oldest'), snapshot(item('2', '2026-09-30T11:00:00Z'), item('3', '2026-09-30T12:00:00Z')));
		s = setFollow(s, false);
		s = push(s, '4', '2026-09-30T13:00:00Z');
		expect(ids(s)).toEqual(['2', '3', '4']);
		expect(s.pending).toBe(0);
	});

	it('a new snapshot clears the count', () => {
		let s = setFollow(base(), false);
		s = push(s, '4', '2026-09-30T13:00:00Z');
		s = run(s, snapshot(item('4', '2026-09-30T13:00:00Z'), item('3', '2026-09-30T12:00:00Z')));
		expect(s.pending).toBe(0);
	});
});

// ── Load older ────────────────────────────────────────────────

/** A database of n items, newest first (id n is the newest). */
function makeDB(n: number): Item[] {
	return Array.from({ length: n }, (_, i) => {
		const id = n - i;
		return item(String(id), new Date(Date.UTC(2026, 0, 1) + id * 3600_000).toISOString());
	});
}

const SNAPSHOT = 200;

/** Stands in for GET /api/items: one page of db at the state's next offset. */
function fetchOlder(s: FeedState, db: Item[]): FeedState {
	const started = beginOlder(s);
	const { offset, limit } = olderRequest(started);
	const rows = db.slice(offset, offset + limit);
	return appendOlder(started, started.epoch, offset, limit, rows, db.length);
}

function loaded(db: Item[]): FeedState {
	return run(initialState(), snapshot(...db.slice(0, SNAPSHOT)));
}

describe('load older', () => {
	it('appends page after page until the end, in order', () => {
		const db = makeDB(450);
		let s = loaded(db);
		expect(canLoadOlder(s)).toBe(true);
		expect(s.ranked).toBe(200);
		s = fetchOlder(s, db);
		expect(s.items).toHaveLength(300);
		expect(s.end).toBe(false);
		s = fetchOlder(s, db);
		expect(s.items).toHaveLength(400);
		expect(s.end).toBe(false);
		// The last page is short, which ends the list.
		s = fetchOlder(s, db);
		expect(s.items).toHaveLength(450);
		expect(s.end).toBe(true);
		expect(canLoadOlder(s)).toBe(false);
		expect(ids(s)).toEqual(db.map((it) => it.id));
	});

	it('has nothing older when the snapshot is shorter than the stream limit', () => {
		const db = makeDB(120);
		const s = loaded(db);
		expect(s.items).toHaveLength(120);
		expect(s.end).toBe(true);
		expect(canLoadOlder(s)).toBe(false);
		expect(run(initialState(), snapshot()).end).toBe(true);
	});

	it('ends when the page is short, or when the total is reached', () => {
		const db = makeDB(250);
		let s = fetchOlder(loaded(db), db);
		expect(s.items).toHaveLength(300 - 50);
		expect(s.end).toBe(true);
		const exact = makeDB(300);
		s = fetchOlder(loaded(exact), exact);
		expect(s.items).toHaveLength(300);
		expect(s.end).toBe(true);
	});

	it('does not load before a snapshot, while buffering, or while loading', () => {
		expect(canLoadOlder(initialState())).toBe(false);
		const db = makeDB(300);
		const s = loaded(db);
		expect(canLoadOlder(reduce(s, { type: 'reset' }))).toBe(false);
		expect(canLoadOlder(beginOlder(s))).toBe(false);
		expect(canLoadOlder(run(initialState(), snapshot()))).toBe(false);
	});

	it('keeps the cursor and the sort order when pages arrive', () => {
		const db = makeDB(400);
		let s = moveCursor(loaded(db), 150);
		const at = ids(s)[150];
		s = fetchOlder(s, db);
		expect(ids(s)[s.cursor]).toBe(at);
		const times = s.items.map((it) => Date.parse(it.published!));
		expect(times).toEqual([...times].sort((a, b) => b - a));
	});

	it('dedupes rows that are already in the list', () => {
		const db = makeDB(400);
		let s = loaded(db);
		s = beginOlder(s);
		// A page that overlaps the list by 10 rows (and repeats one inside itself).
		const rows = db.slice(190, 290);
		const page = [...rows, rows[50]!];
		s = appendOlder(s, s.epoch, 190, 100, page, db.length);
		expect(s.items).toHaveLength(290);
		expect(new Set(ids(s)).size).toBe(290);
	});

	it('a live push above the loaded range moves the next offset down by one', () => {
		const db = makeDB(400);
		let s = loaded(db);
		const fresh = item('401', '2027-01-01T00:00:00Z');
		s = reduce(s, { type: 'item', item: fresh });
		expect(s.ranked).toBe(201);
		const db2 = [fresh, ...db];
		s = fetchOlder(s, db2);
		expect(ids(s)).toEqual(db2.slice(0, 301).map((it) => it.id));
		expect(s.ranked).toBe(301);
	});

	it('a push that sorts after the loaded range is kept, not counted, and never skips a row', () => {
		const db = makeDB(400);
		let s = loaded(db);
		// Published long ago: it belongs near the very end of the database.
		const old = item('900', '2026-01-01T00:30:00Z');
		s = reduce(s, { type: 'item', item: old });
		expect(ids(s)[200]).toBe('900');
		expect(s.ranked).toBe(200);
		const db2 = [...db, old].sort((a, b) => compareItems(a, b, 'newest'));
		s = fetchOlder(s, db2);
		// The pushed row stays last, after the rows the page brought.
		expect(s.items).toHaveLength(301);
		expect(ids(s).slice(0, 300)).toEqual(db2.slice(0, 300).map((it) => it.id));
		expect(ids(s)[300]).toBe('900');
		// Paging on reaches the end with every row once and no gaps.
		for (let i = 0; i < 5 && !s.end; i++) s = fetchOlder(s, db2);
		expect(s.end).toBe(true);
		expect(ids(s)).toEqual(db2.map((it) => it.id));
	});

	it('an older-dated push into the middle of the loaded range counts as ranked', () => {
		const db = makeDB(400);
		let s = loaded(db);
		const mid = item('901', db[100]!.published);
		s = reduce(s, { type: 'item', item: mid });
		expect(s.ranked).toBe(201);
		const db2 = [...db.slice(0, 101), mid, ...db.slice(101)];
		s = fetchOlder(s, db2);
		expect(ids(s)).toEqual(db2.slice(0, 301).map((it) => it.id));
	});

	it('a push while a page is in flight never skips a row (either side of the query)', () => {
		const db = makeDB(400);
		const fresh = item('401', '2027-01-01T00:00:00Z');
		const db2 = [fresh, ...db];

		// Pushed after the database answered: the page is from before the push.
		let s = beginOlder(loaded(db));
		const req = olderRequest(s);
		const before = db.slice(req.offset, req.offset + req.limit);
		s = reduce(s, { type: 'item', item: fresh });
		s = appendOlder(s, s.epoch, req.offset, req.limit, before, db.length);
		s = fetchOlder(s, db2);
		expect(ids(s)).toEqual(db2.slice(0, ids(s).length).map((it) => it.id));
		expect(s.items.length).toBeGreaterThanOrEqual(400);

		// Pushed before the database answered: the page already reflects it.
		s = beginOlder(loaded(db));
		const req2 = olderRequest(s);
		const after = db2.slice(req2.offset, req2.offset + req2.limit);
		s = reduce(s, { type: 'item', item: fresh });
		s = appendOlder(s, s.epoch, req2.offset, req2.limit, after, db2.length);
		s = fetchOlder(s, db2);
		expect(ids(s)).toEqual(db2.slice(0, ids(s).length).map((it) => it.id));
		expect(s.items.length).toBeGreaterThanOrEqual(400);
	});

	it('a reset drops the older pages and a late answer is ignored', () => {
		const db = makeDB(450);
		let s = fetchOlder(loaded(db), db);
		expect(s.items).toHaveLength(300);

		// A page is in flight when the connection resets.
		s = beginOlder(s);
		const { epoch } = s;
		const req = olderRequest(s);
		s = run(s, snapshot(...db.slice(0, SNAPSHOT)));
		expect(s.items).toHaveLength(200);
		expect(s.ranked).toBe(200);
		expect(s.end).toBe(false);
		expect(s.loading).toBe(false);
		const late = appendOlder(s, epoch, req.offset, req.limit, db.slice(req.offset, req.offset + req.limit), db.length);
		expect(late).toBe(s);
		// And the list can load again from the snapshot.
		s = fetchOlder(s, db);
		expect(s.items).toHaveLength(300);
	});

	it('keeps the cursor on its item across a reset when the snapshot has it', () => {
		const db = makeDB(450);
		let s = fetchOlder(loaded(db), db);
		s = moveCursor(s, 120);
		const at = ids(s)[120];
		s = run(s, snapshot(...db.slice(0, SNAPSHOT)));
		expect(ids(s)[s.cursor]).toBe(at);
		// From deeper than the snapshot reaches, it clamps to the last row.
		s = fetchOlder(s, db);
		s = moveCursor(s, 280);
		s = run(s, snapshot(...db.slice(0, SNAPSHOT)));
		expect(s.cursor).toBe(199);
	});

	it('ignores an answer while buffering a new snapshot', () => {
		const db = makeDB(450);
		let s = beginOlder(loaded(db));
		const req = olderRequest(s);
		const epoch = s.epoch;
		s = reduce(s, { type: 'reset' });
		const r = appendOlder(s, epoch, req.offset, req.limit, db.slice(200, 300), db.length);
		expect(r).toBe(s);
	});

	it('a failed request can be tried again, unless a snapshot replaced it', () => {
		const db = makeDB(450);
		let s = beginOlder(loaded(db));
		const epoch = s.epoch;
		s = failOlder(s, epoch);
		expect(s.loading).toBe(false);
		expect(canLoadOlder(s)).toBe(true);
		expect(failOlder(s, epoch + 1)).toBe(s);
	});

	it('works oldest first too', () => {
		const db = makeDB(300).reverse();
		let s = run(initialState('oldest'), snapshot(...db.slice(0, SNAPSHOT)));
		s = fetchOlder(s, db);
		expect(ids(s)).toEqual(db.map((it) => it.id));
		expect(s.end).toBe(true);
	});
});

describe('load older with an unviewed-only filter', () => {
	const unviewedDB = (n: number) => makeDB(n);

	it('takes rows marked viewed off the offset', () => {
		const db = unviewedDB(450);
		let s = { ...loaded(db), unviewedOnly: true };
		expect(s.ranked).toBe(200);
		// 30 rows on screen were marked viewed, so they left the database result.
		const seen = new Set(ids(s).slice(0, 30));
		s = markViewed(s, seen);
		expect(s.ranked).toBe(170);
		const remaining = db.filter((it) => !seen.has(it.id));
		s = fetchOlder(s, remaining);
		// No row of the database result is missing from the list.
		const have = new Set(ids(s));
		for (const it of remaining.slice(0, 300 - 30)) expect(have.has(it.id), it.id).toBe(true);
	});

	it('overlaps the request so views the browser has not seen yet cannot skip rows', () => {
		const db = unviewedDB(450);
		let s = { ...loaded(db), unviewedOnly: true };
		expect(olderRequest(s)).toEqual({ offset: 150, limit: PAGE_SIZE + 50 });
		// The daemon has already dropped 20 viewed rows; the browser does not know yet.
		const gone = new Set(ids(s).slice(0, 20));
		const remaining = db.filter((it) => !gone.has(it.id));
		s = fetchOlder(s, remaining);
		const have = new Set(ids(s));
		for (const it of remaining.slice(0, 280)) expect(have.has(it.id), it.id).toBe(true);
	});

	it('without the filter, viewing changes nothing about the offset', () => {
		const db = makeDB(300);
		let s = loaded(db);
		s = markViewed(s, new Set(ids(s).slice(0, 30)));
		expect(s.ranked).toBe(200);
		expect(olderRequest(s)).toEqual({ offset: 200, limit: PAGE_SIZE });
	});

	it('does not count rows that were already viewed, or below the ranked range', () => {
		const db = unviewedDB(300);
		let s = { ...loaded(db), unviewedOnly: true };
		s = markViewed(s, new Set(['1']));
		expect(markViewed(s, new Set(['1']))).toBe(s);
	});
});

describe('update events (assessments)', () => {
	const as = (assessor: string, score: number, tag?: string) => ({
		id: `${assessor}${tag ?? ''}`,
		assessor_id: assessor,
		assessor_name: 'claude',
		tag_id: tag,
		score
	});
	const scored = (id: string, published: string, ...a: ReturnType<typeof as>[]): Item => item(id, published, { assessments: a });
	const T = (n: number) => `2026-09-30T1${n}:00:00Z`;

	it('replaces an item it shows in place, keeping its position and viewed state', () => {
		let s = run(initialState(), snapshot(item('1', T(3)), item('2', T(2), { viewed: true }), item('3', T(1))));
		s = reduce(s, { type: 'update', item: scored('2', T(2), as('1', 0.9)), matches: false });
		expect(ids(s)).toEqual(['1', '2', '3']);
		expect(s.items[1]!.assessments?.[0]?.score).toBe(0.9);
		expect(s.items[1]!.viewed).toBe(true);
	});

	it('inserts an item that now matches, in sort order, and ignores one that does not', () => {
		let s = run(initialState(), snapshot(item('1', T(3)), item('3', T(1))));
		const before = s;
		expect(reduce(s, { type: 'update', item: scored('2', T(2), as('1', 0.9)), matches: false })).toBe(before);
		s = reduce(s, { type: 'update', item: scored('2', T(2), as('1', 0.9)), matches: true });
		expect(ids(s)).toEqual(['1', '2', '3']);
		expect(s.ranked).toBe(3);
	});

	it('never removes an item, whether or not it matches', () => {
		let s = run(initialState(), snapshot(item('1', T(3)), item('2', T(2))));
		s = reduce(s, { type: 'update', item: scored('1', T(3), as('1', 0.1)), matches: false });
		expect(ids(s)).toEqual(['1', '2']);
	});

	it('keeps the cursor on its item when an update inserts above it', () => {
		let s = run(initialState(), snapshot(item('1', T(3)), item('3', T(1))));
		s = setFollow(moveCursor(s, 1), false);
		s = reduce(s, { type: 'update', item: scored('2', T(2), as('1', 0.9)), matches: true });
		expect(ids(s)).toEqual(['1', '2', '3']);
		expect(s.items[s.cursor]!.id).toBe('3');
		expect(s.pending).toBe(1);
	});

	it('during a snapshot updates the buffered item, or adds a match in order', () => {
		let s = run(initialState(), [{ type: 'reset' }, { type: 'item', item: item('1', T(3)) }, { type: 'item', item: item('3', T(1)) }]);
		s = reduce(s, { type: 'update', item: scored('1', T(3), as('1', 0.5)), matches: true });
		s = reduce(s, { type: 'update', item: scored('2', T(2), as('1', 0.6)), matches: true });
		s = reduce(s, { type: 'update', item: scored('9', T(0), as('1', 0.6)), matches: false });
		s = reduce(s, { type: 'complete' });
		expect(ids(s)).toEqual(['1', '2', '3']);
		expect(s.items[0]!.assessments).toHaveLength(1);
	});
});

describe('the score sort', () => {
	const as = (assessor: string, score: number | undefined, tag?: string) => ({
		id: `${assessor}${tag ?? ''}${score}`,
		assessor_id: assessor,
		tag_id: tag,
		score
	});
	const scored = (id: string, published: string, ...a: ReturnType<typeof as>[]): Item => item(id, published, { assessments: a });
	const T = (n: number) => `2026-09-30T1${n}:00:00Z`;
	const score = { assessor: '1', tags: null };

	it('orders by the assessor score, unscored last and newest first among themselves', () => {
		const items = [
			scored('lo', T(9), as('1', 0.2)),
			scored('none-old', T(1)),
			scored('hi', T(1), as('1', 0.9)),
			scored('none-new', T(5)),
			scored('other', T(9), as('2', 1)),
			scored('note', T(8), as('1', undefined))
		];
		const sorted = [...items].sort((a, b) => compareItems(a, b, 'score', score));
		expect(sorted.map((x) => x.id)).toEqual(['hi', 'lo', 'other', 'note', 'none-new', 'none-old']);
	});

	it('equal scores keep newest first', () => {
		const a = scored('a', T(1), as('1', 0.5));
		const b = scored('b', T(2), as('1', 0.5));
		expect(compareItems(a, b, 'score', score)).toBeGreaterThan(0);
	});

	it('uses the scope: only in-scope scores count', () => {
		const x = scored('x', T(1), as('1', 0.2), as('1', 0.9, '7'));
		const y = scored('y', T(1), as('1', 0.5));
		const inTag = { assessor: '1', tags: new Set(['4']) };
		expect(compareItems(x, y, 'score', score)).toBeLessThan(0);
		expect(compareItems(x, y, 'score', inTag)).toBeGreaterThan(0);
	});

	it('a new item (no assessments) goes after the scored ones, before older unscored', () => {
		let s = initialState('score', score);
		s = run(s, snapshot(scored('hi', T(1), as('1', 0.9)), scored('lo', T(1), as('1', 0.1)), scored('old', T(1))));
		s = reduce(s, { type: 'item', item: item('fresh', T(5)) });
		expect(ids(s)).toEqual(['hi', 'lo', 'fresh', 'old']);
		expect(s.pending).toBe(0);
	});

	it('an update that starts matching is placed by its score', () => {
		let s = initialState('score', score);
		s = run(s, snapshot(scored('hi', T(1), as('1', 0.9)), scored('lo', T(1), as('1', 0.1))));
		s = reduce(s, { type: 'update', item: scored('mid', T(0), as('1', 0.5)), matches: true });
		expect(ids(s)).toEqual(['hi', 'mid', 'lo']);
		// Better than all: at the top.
		s = reduce(s, { type: 'update', item: scored('best', T(0), as('1', 1)), matches: true });
		expect(ids(s)).toEqual(['best', 'hi', 'mid', 'lo']);
	});

	it('an older page follows the rows already loaded', () => {
		let s = initialState('score', score);
		s = run(s, snapshot(scored('a', T(1), as('1', 0.9)), scored('d', T(1), as('1', 0.2))));
		s = beginOlder(s);
		s = appendOlder(s, s.epoch, s.ranked, 100, [scored('b', T(1), as('1', 0.5)), scored('c', T(1))], 4);
		expect(ids(s)).toEqual(['a', 'd', 'b', 'c']);
	});
});
