import { safeColor } from './sanitize';
import type { Source, Tag } from './types';

/** How a source is shown in a row: its abbreviation (or name) and color. */
export interface SourceDisplay {
	label: string;
	color: string | undefined;
}

export function sourceDisplays(sources: Source[]): Map<string, SourceDisplay> {
	const m = new Map<string, SourceDisplay>();
	for (const s of sources) {
		if (!s.id) continue;
		m.set(s.id, { label: s.abbreviation || s.name || '', color: safeColor(s.color) });
	}
	return m;
}

/** How a tag is shown in a chip: its current name and color. */
export interface TagDisplay {
	name: string;
	color: string | undefined;
}

/**
 * Tags by ID. An item carries a copy of its tags from when the feed was
 * loaded; rows prefer these, so a renamed or recolored tag shows its new
 * look without reloading the feed.
 */
export function tagDisplays(tags: Tag[]): Map<string, TagDisplay> {
	const m = new Map<string, TagDisplay>();
	for (const t of tags) {
		if (t.id) m.set(t.id, { name: t.name ?? '', color: safeColor(t.color) });
	}
	return m;
}

/**
 * When an enabled source is next fetched, in ms since epoch: its last fetch
 * plus its refresh interval, the schedule the daemon keeps. A source that
 * was never fetched is due now. Null for a disabled source.
 */
export function nextFetch(s: Source, now: number): number | null {
	if (!s.enabled) return null;
	const lf = s.last_fetch ? Date.parse(s.last_fetch) : NaN;
	return Number.isNaN(lf) ? now : lf + (s.refresh_sec ?? 0) * 1000;
}

export interface FetchTimes {
	/** Most recent fetch of any source, ms since epoch. */
	last: number | null;
	/** Earliest next scheduled fetch of an enabled source, ms since epoch. */
	next: number | null;
}

/**
 * Last and next fetch times for the status bar. The next fetch is estimated
 * from each enabled source's last fetch plus its refresh interval, the same
 * schedule the daemon keeps.
 */
export function fetchTimes(sources: Source[], now: number): FetchTimes {
	let last: number | null = null;
	let next: number | null = null;
	for (const s of sources) {
		const lf = s.last_fetch ? Date.parse(s.last_fetch) : NaN;
		if (!Number.isNaN(lf)) last = last === null ? lf : Math.max(last, lf);
		const n = nextFetch(s, now);
		if (n !== null) next = next === null ? n : Math.min(next, n);
	}
	return { last, next };
}
