// The /api/stream connection: EventSource lifecycle, connection status and
// the feed state the reducer maintains.
//
// A filter change closes the EventSource and opens a new one with new query
// parameters; there is no browser-to-server message. On any error the
// connection is closed and reopened with backoff, and the server sends
// reset + a fresh snapshot, which the reducer applies idempotently.

import { filterQuery } from './filter';
import { initialState, reduce, type FeedState, type StreamEvent } from './reducer';
import type { Filter, Item } from './types';

export type ConnStatus = 'connecting' | 'connected' | 'disconnected';

const MIN_BACKOFF_MS = 1000;
const MAX_BACKOFF_MS = 30000;

export class FeedStream {
	/** Connection state for the status bar. */
	status: ConnStatus = $state('connecting');
	/** Feed state; replaced (not mutated) on every change that matters. */
	state: FeedState = $state.raw(initialState());

	private es: EventSource | null = null;
	private filter: Filter | null = null;
	private retryTimer: ReturnType<typeof setTimeout> | null = null;
	private backoff = MIN_BACKOFF_MS;
	private listeners = new Set<(prev: FeedState, next: FeedState) => void>();

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
		// Keep the rows on screen until the new snapshot is complete.
		this.state = { ...this.state, sort: f.sort };
		this.open();
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
		const es = new EventSource('/api/stream' + filterQuery(this.filter));
		this.es = es;

		es.addEventListener('open', () => {
			if (this.es === es) this.status = 'connected';
		});
		es.addEventListener('reset', () => {
			if (this.es === es) this.apply({ type: 'reset' });
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
