// Typed wrappers for the nyttig-api JSON API. Responses are protojson; see
// types.ts. Mutations send JSON with the page's own Origin, which is what
// nyttig-api's CSRF check requires.

import { filterToParams } from './filter';
import type { Assessment, Assessor, Filter, Item, RuleTest, SavedView, Source, Tag, TagRule, ViewFilter } from './types';

export class ApiError extends Error {
	constructor(
		readonly status: number,
		message: string
	) {
		super(message);
	}
}

/** True when the daemon says the thing (source, tag, rule, view) does not exist. */
export function isNotFound(e: unknown): boolean {
	return e instanceof ApiError && e.status === 404;
}

async function request<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
	const init: RequestInit = { method, headers: { Accept: 'application/json' }, cache: 'no-store', signal };
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

// ── Management ────────────────────────────────────────────────
// Bodies are built by forms.ts. PATCH bodies carry only the fields that
// change: nyttig-api leaves absent fields unchanged.

export interface SourceBody {
	name?: string;
	url?: string;
	type?: string;
	refresh_sec?: number;
	enabled?: boolean;
	color?: string;
	abbreviation?: string;
}

export interface TagBody {
	name?: string;
	color?: string;
	/** On PATCH this replaces the parents; [] makes the tag top-level. */
	parent_ids?: string[];
}

export interface RuleBody {
	tag_id: string;
	/** "" or absent: a global rule. */
	source_id?: string;
	field: string;
	pattern: string;
	priority?: number;
}

export interface AssessorBody {
	name?: string;
	description?: string;
	color?: string;
}

export interface AssessmentBody {
	/** Assessor ID. */
	assessor: string;
	/** Tag ID; absent for the item as a whole. */
	tag?: string;
	/** From 0 to 1; absent for no score (0 is a score). */
	score?: number;
	note?: string;
}

export interface ViewBody {
	name?: string;
	/** On PATCH this replaces the whole filter. */
	filter?: ViewFilter;
	favorite?: boolean;
}

const idPath = (base: string, id: string) => base + '/' + encodeURIComponent(id);

export function addSource(body: SourceBody): Promise<Source> {
	return request<Source>('POST', '/api/sources', body);
}

export function updateSource(id: string, patch: SourceBody): Promise<Source> {
	return request<Source>('PATCH', idPath('/api/sources', id), patch);
}

export function removeSource(id: string): Promise<void> {
	return request<void>('DELETE', idPath('/api/sources', id));
}

export function addTag(body: TagBody): Promise<Tag> {
	return request<Tag>('POST', '/api/tags', body);
}

export function updateTag(id: string, patch: TagBody): Promise<Tag> {
	return request<Tag>('PATCH', idPath('/api/tags', id), patch);
}

export function removeTag(id: string): Promise<void> {
	return request<void>('DELETE', idPath('/api/tags', id));
}

export async function listAssessors(): Promise<Assessor[]> {
	return (await request<{ assessors?: Assessor[] }>('GET', '/api/assessors')).assessors ?? [];
}

export function addAssessor(body: AssessorBody): Promise<Assessor> {
	return request<Assessor>('POST', '/api/assessors', body);
}

export function updateAssessor(id: string, patch: AssessorBody): Promise<Assessor> {
	return request<Assessor>('PATCH', idPath('/api/assessors', id), patch);
}

export function removeAssessor(id: string): Promise<void> {
	return request<void>('DELETE', idPath('/api/assessors', id));
}

/** Stores an assessment, replacing the same assessor's earlier one for the same tag. */
export function putAssessment(itemId: string, body: AssessmentBody): Promise<Assessment> {
	return request<Assessment>('PUT', idPath('/api/items', itemId) + '/assessments', body);
}

export function removeAssessment(itemId: string, assessor: string, tag = ''): Promise<void> {
	const p = new URLSearchParams({ assessor });
	if (tag) p.set('tag', tag);
	return request<void>('DELETE', idPath('/api/items', itemId) + '/assessments?' + p.toString());
}

export async function listViews(): Promise<SavedView[]> {
	return (await request<{ views?: SavedView[] }>('GET', '/api/views')).views ?? [];
}

export function addView(body: ViewBody): Promise<SavedView> {
	return request<SavedView>('POST', '/api/views', body);
}

export function updateView(id: string, patch: ViewBody): Promise<SavedView> {
	return request<SavedView>('PATCH', idPath('/api/views', id), patch);
}

export function removeView(id: string): Promise<void> {
	return request<void>('DELETE', idPath('/api/views', id));
}

/** Sets the display order; ids must list every view once. Answers with the views in the new order. */
export async function reorderViews(ids: string[]): Promise<SavedView[]> {
	return (await request<{ views?: SavedView[] }>('PUT', '/api/views/order', { ids })).views ?? [];
}

export async function listRules(): Promise<TagRule[]> {
	return (await request<{ rules?: TagRule[] }>('GET', '/api/rules')).rules ?? [];
}

export function addRule(body: RuleBody): Promise<TagRule> {
	return request<TagRule>('POST', '/api/rules', body);
}

export function removeRule(id: string): Promise<void> {
	return request<void>('DELETE', idPath('/api/rules', id));
}

/**
 * Dry-runs a pattern on the daemon, which matches with Go RE2 exactly like
 * the tagger. Patterns are never evaluated in the browser: JavaScript
 * regular expressions differ (e.g. "(?i)" is a syntax error there).
 */
export async function testRule(
	q: { pattern: string; field: string; source_id?: string; limit?: number },
	signal?: AbortSignal
): Promise<RuleTest> {
	const body: Record<string, unknown> = { pattern: q.pattern, field: q.field };
	if (q.source_id) body.source_id = q.source_id;
	if (q.limit) body.limit = q.limit;
	const r = await request<{ items?: Item[]; scanned?: number }>('POST', '/api/rules/test', body, signal);
	return { items: r.items ?? [], scanned: r.scanned ?? 0 };
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

/**
 * With exact, a tag filter matches that tag only, not the tags below it
 * (the tags view counts what deleting a tag removes this way).
 */
export async function searchItems(f: Filter, limit: number, offset = 0, exact = false): Promise<Page> {
	const p = filterToParams(f);
	if (exact && f.tag) p.set('tag_exact', '1');
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
