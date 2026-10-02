// The /api/stream connection: EventSource lifecycle, connection status and
// the feed state the reducer maintains.
//
// A filter change closes the EventSource and opens a new one with new query
// parameters; there is no browser-to-server message. On any error the
// connection is closed and reopened with backoff, and the server sends
// reset + a fresh snapshot, which the reducer applies idempotently.

import { searchItems } from './api';
import { apiParams } from './filter';
import {
	appendOlder,
	beginOlder,
	canLoadOlder,
	failOlder,
	initialState,
	olderRequest,
	reduce,
	type FeedState,
	type StreamEvent
} from './reducer';
import { sinceAfter } from './since';
import type { Filter, Item } from './types';

export type ConnStatus = 'connecting' | 'connected' | 'disconnected';

const MIN_BACKOFF_MS = 1000;
const MAX_BACKOFF_MS = 30000;
/** After a failed page, wait this long before the next scroll tries again. */
const OLDER_RETRY_MS = 5000;

export class FeedStream {
	/** Connection state for the status bar. */
	status: ConnStatus = $state('connecting');
	/** Feed state; replaced (not mutated) on every change that matters. */
	state: FeedState = $state.raw(initialState());

	private es: EventSource | null = null;
	private filter: Filter | null = null;
	/**
	 * The cutoff of the current snapshot in unix seconds, or undefined: the
	 * filter's window counted back from when the stream last (re)connected.
	 * The stream and every older page send this same value, so rows do not
	 * move between them; rows age out of the list only on the next connect.
	 */
	after: number | undefined = undefined;
	/**
	 * The cutoff the stream was last opened with. After a reconnect it only
	 * replaces `after` when the daemon's reset arrives: until then the rows on
	 * screen are the old snapshot's, and an older page for them must keep the
	 * old cutoff.
	 */
	private openedAfter: number | undefined = undefined;
	private retryTimer: ReturnType<typeof setTimeout> | null = null;
	private backoff = MIN_BACKOFF_MS;
	private listeners = new Set<(prev: FeedState, next: FeedState) => void>();
	private olderRetryAt = 0;
	/** The last "load older" failure, or "" (shown in the list's footer). */
	olderError = $state('');

	/** Applies an event and notifies listeners when the state changed. */
	apply(ev: StreamEvent): void {
		const prev = this.state;
		const next = reduce(prev, ev);
		if (next !== prev || ev.type === 'complete') {
			this.state = next;
			for (const l of this.listeners) l(prev, next);
		}
	}

	/** Replaces the state outside of stream events (cursor moves, viewed). */
	set(next: FeedState): void {
		const prev = this.state;
		if (next === prev) return;
		this.state = next;
		for (const l of this.listeners) l(prev, next);
	}

	onChange(fn: (prev: FeedState, next: FeedState) => void): () => void {
		this.listeners.add(fn);
		return () => this.listeners.delete(fn);
	}

	/** Connects with filter f, replacing any current connection. */
	connect(f: Filter): void {
		this.filter = f;
		this.backoff = MIN_BACKOFF_MS;
		// Keep the rows on screen until the new snapshot is complete, but
		// stop treating them as loaded: an older page requested for the
		// previous filter must not land in the new list, and nothing
		// more is loaded (or marked viewed) until the snapshot is in.
		const s = this.state;
		this.state = {
			...s,
			sort: f.sort,
			unviewedOnly: f.unviewed,
			complete: false,
			loading: false,
			epoch: s.epoch + 1
		};
		this.olderRetryAt = 0;
		this.olderError = '';
		this.open();
		// A new filter starts a new snapshot at once: nothing is loaded.
		this.after = this.openedAfter;
	}

	/**
	 * Fetches the next page below the list with GET /api/items (the same
	 * filter, at the offset the reducer tracks) and merges it in. Does
	 * nothing while a page is out, at the end, or shortly after a failure.
	 */
	async loadOlder(): Promise<void> {
		const f = this.filter;
		const s = this.state;
		if (!f || !canLoadOlder(s) || Date.now() < this.olderRetryAt) return;
		const { offset, limit } = olderRequest(s);
		const epoch = s.epoch;
		this.set(beginOlder(s));
		try {
			const page = await searchItems(f, limit, offset, false, this.after);
			this.olderError = '';
			this.set(appendOlder(this.state, epoch, offset, limit, page.items, page.total));
		} catch (e) {
			this.olderRetryAt = Date.now() + OLDER_RETRY_MS;
			this.olderError = e instanceof Error ? e.message : String(e);
			this.set(failOlder(this.state, epoch));
		}
	}

	close(): void {
		this.filter = null;
		this.teardown();
	}

	private teardown(): void {
		if (this.retryTimer !== null) clearTimeout(this.retryTimer);
		this.retryTimer = null;
		this.es?.close();
		this.es = null;
	}

	private open(): void {
		this.teardown();
		if (!this.filter) return;
		this.status = 'connecting';
		// A new snapshot: take the cutoff again, now.
		this.openedAfter = sinceAfter(this.filter.since, Date.now());
		const q = apiParams(this.filter, this.openedAfter).toString();
		const es = new EventSource('/api/stream' + (q ? '?' + q : ''));
		this.es = es;

		es.addEventListener('open', () => {
			if (this.es === es) this.status = 'connected';
		});
		es.addEventListener('reset', () => {
			if (this.es !== es) return;
			this.after = this.openedAfter;
			this.apply({ type: 'reset' });
		});
		es.addEventListener('item', (e) => {
			if (this.es !== es) return;
			let item: Item;
			try {
				item = JSON.parse((e as MessageEvent<string>).data) as Item;
			} catch {
				return;
			}
			if (typeof item?.id !== 'string') return;
			this.apply({ type: 'item', item });
		});
		es.addEventListener('complete', () => {
			if (this.es !== es) return;
			this.backoff = MIN_BACKOFF_MS;
			this.apply({ type: 'complete' });
		});
		es.addEventListener('error', () => {
			if (this.es !== es) return;
			// Covers both an HTTP error (daemon down: EventSource gives up)
			// and a dropped connection (overflow, restart). Reconnect
			// ourselves either way, with backoff.
			this.status = 'disconnected';
			es.close();
			this.es = null;
			const delay = this.backoff;
			this.backoff = Math.min(this.backoff * 2, MAX_BACKOFF_MS);
			this.retryTimer = setTimeout(() => this.open(), delay);
		});
	}
}
