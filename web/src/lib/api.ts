// Typed wrappers for the nyttig-api JSON API. Responses are protojson; see
// types.ts. Mutations send JSON with the page's own Origin, which is what
// nyttig-api's CSRF check requires.

import { filterToParams } from './filter';
import type { Filter, Item, Source, Tag } from './types';

export class ApiError extends Error {
	constructor(
		readonly status: number,
		message: string
	) {
		super(message);
	}
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
	const init: RequestInit = { method, headers: { Accept: 'application/json' }, cache: 'no-store' };
	if (method !== 'GET') {
		init.headers = { ...init.headers, 'Content-Type': 'application/json' };
		init.body = JSON.stringify(body ?? {});
	}
	const res = await fetch(path, init);
	if (!res.ok) {
		let msg = res.statusText;
		try {
			const j = (await res.json()) as { error?: string };
			if (j.error) msg = j.error;
		} catch {
			// Not JSON (e.g. from the reverse proxy).
		}
		throw new ApiError(res.status, msg);
	}
	if (res.status === 204) return undefined as T;
	return (await res.json()) as T;
}

export async function listSources(): Promise<Source[]> {
	return (await request<{ sources?: Source[] }>('GET', '/api/sources')).sources ?? [];
}

export async function listTags(): Promise<Tag[]> {
	return (await request<{ tags?: Tag[] }>('GET', '/api/tags')).tags ?? [];
}

/** Fetches all sources now, or one when sourceId is given. */
export async function refresh(sourceId?: string): Promise<void> {
	const q = sourceId ? '?source=' + encodeURIComponent(sourceId) : '';
	await request<void>('POST', '/api/refresh' + q);
}

export interface Page {
	items: Item[];
	total: number;
}

export async function searchItems(f: Filter, limit: number, offset = 0): Promise<Page> {
	const p = filterToParams(f);
	p.set('limit', String(limit));
	if (offset) p.set('offset', String(offset));
	const r = await request<{ items?: Item[]; total?: number }>('GET', '/api/items?' + p.toString());
	return { items: r.items ?? [], total: r.total ?? 0 };
}

/** The most IDs sent in one /api/viewed request (the server allows 1000). */
const VIEWED_BATCH = 500;

function batches(ids: string[]): string[][] {
	const out: string[][] = [];
	for (let i = 0; i < ids.length; i += VIEWED_BATCH) out.push(ids.slice(i, i + VIEWED_BATCH));
	return out;
}

export async function markViewed(ids: string[]): Promise<void> {
	for (const b of batches(ids)) await request<void>('POST', '/api/viewed', { ids: b });
}

/**
 * Sends viewed IDs while the page is going away. sendBeacon survives the
 * unload; a keepalive fetch is the fallback where the browser refuses a
 * JSON beacon.
 */
export function beaconViewed(ids: string[]): void {
	for (const b of batches(ids)) {
		const body = JSON.stringify({ ids: b });
		let queued = false;
		try {
			queued = navigator.sendBeacon('/api/viewed', new Blob([body], { type: 'application/json' }));
		} catch {
			queued = false;
		}
		if (!queued) {
			void fetch('/api/viewed', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body,
				keepalive: true
			}).catch(() => {});
		}
	}
}
