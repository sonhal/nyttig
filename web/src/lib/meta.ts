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

/** Tag colors by name, for chips whose item copy has no color. */
export function tagColors(tags: Tag[]): Map<string, string> {
	const m = new Map<string, string>();
	for (const t of tags) {
		const c = safeColor(t.color);
		if (t.name && c) m.set(t.name, c);
	}
	return m;
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
		if (!s.enabled) continue;
		const n = Number.isNaN(lf) ? now : lf + (s.refresh_sec ?? 0) * 1000;
		next = next === null ? n : Math.min(next, n);
	}
	return { last, next };
}
