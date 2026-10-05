// Digests: documents an assessor wrote about many items, kept in series (see
// docs/digests-plan.md). The pure parts of the digests page live here: the
// URL (/digests?series=<id>&digest=<id>, the source of truth), grouping the
// series by assessor, walking through a series' history, and the numbers the
// delete confirmations quote. Titles, names and bodies are untrusted text:
// nothing here builds markup.

import { oneLine } from './sanitize';
import type { Digest, DigestSeries } from './types';

const ID_RE = /^[1-9][0-9]{0,18}$/;

export interface DigestSelection {
	/** Series ID, "" = the first series. */
	series: string;
	/** Digest ID, "" = the newest of the series. */
	digest: string;
}

/** The selection a URL asks for; anything that is not an ID counts as absent. */
export function selectionFromParams(p: URLSearchParams): DigestSelection {
	const id = (k: string) => {
		const v = p.get(k) ?? '';
		return ID_RE.test(v) ? v : '';
	};
	return { series: id('series'), digest: id('digest') };
}

/** "/digests?series=3&digest=9"; with no digest, the newest of the series. */
export function digestsHref(series = '', digest = ''): string {
	const p = new URLSearchParams();
	if (series) p.set('series', series);
	if (digest) p.set('digest', digest);
	const s = p.toString();
	return '/digests' + (s ? '?' + s : '');
}

// ── Series ────────────────────────────────────────────────────

/** "claude/daily-cve" on one line. */
export function seriesLabel(s: DigestSeries): string {
	return `${oneLine(s.assessor_name)}/${oneLine(s.name)}`;
}

export interface SeriesGroup {
	assessorId: string;
	assessorName: string;
	series: DigestSeries[];
}

/**
 * The series grouped by assessor. The list arrives in display order
 * (position); a group stands where its first series does, so reordering
 * series moves them within their assessor and moves whole groups only when a
 * group's first series moves.
 */
export function groupSeries(series: readonly DigestSeries[]): SeriesGroup[] {
	const groups: SeriesGroup[] = [];
	const byAssessor = new Map<string, SeriesGroup>();
	for (const s of series) {
		const key = s.assessor_id ?? '';
		let g = byAssessor.get(key);
		if (!g) {
			g = { assessorId: key, assessorName: s.assessor_name ?? '', series: [] };
			byAssessor.set(key, g);
			groups.push(g);
		}
		g.series.push(s);
	}
	return groups;
}

/** The series in the order the page lists them: group by group. */
export function seriesInListOrder(series: readonly DigestSeries[]): DigestSeries[] {
	return groupSeries(series).flatMap((g) => g.series);
}

/** The series after (dir 1) or before (-1) the one with this ID, in list order, or undefined at the ends. */
export function adjacentSeries(series: readonly DigestSeries[], id: string, dir: 1 | -1): DigestSeries | undefined {
	const list = seriesInListOrder(series);
	const i = list.findIndex((s) => s.id === id);
	if (i < 0) return dir === 1 ? list[0] : list[list.length - 1];
	return list[i + dir];
}

/**
 * The full display order after moving a series one place up (dir -1) or down
 * (1) among its own assessor's series, as the IDs the reorder call takes, or
 * null when it is already at that end of its group. The list is in display
 * order; the series that swap places are the series' neighbors in the group,
 * so a move never pulls a series into another assessor's group.
 */
export function moveWithinAssessor(series: readonly DigestSeries[], id: string, dir: 1 | -1): string[] | null {
	const me = series.find((s) => s.id === id);
	if (!me) return null;
	const group = series.filter((s) => s.assessor_id === me.assessor_id);
	const i = group.findIndex((s) => s.id === id);
	const other = group[i + dir];
	if (!other?.id) return null;
	const ids = series.map((s) => s.id ?? '');
	const a = ids.indexOf(id);
	const b = ids.indexOf(other.id);
	[ids[a], ids[b]] = [ids[b] ?? '', ids[a] ?? ''];
	return ids;
}

/** The series the page shows for a selection: the one asked for, else the first. */
export function currentSeries(series: readonly DigestSeries[], id: string): DigestSeries | undefined {
	return series.find((s) => s.id === id) ?? seriesInListOrder(series)[0];
}

/** The names the series are completed and found by: "assessor/name" with whitespace collapsed. */
export function seriesNames(series: readonly DigestSeries[]): string[] {
	return series.map((s) => seriesLabel(s));
}

export type FindSeries = { ok: true; id: string } | { ok: false; error: string };

/**
 * Finds a series by "assessor/name" (any case), or by its name alone when
 * only one assessor has a series of that name. Used by ":digest".
 */
export function findSeries(series: readonly DigestSeries[], ref: string): FindSeries {
	const want = ref.trim().replace(/\s+/g, ' ').toLowerCase();
	if (!want) return { ok: false, error: 'digest: <assessor>/<series>' };
	const full = series.filter((s) => seriesLabel(s).toLowerCase() === want);
	if (full.length === 1 && full[0]?.id) return { ok: true, id: full[0].id };
	const byName = series.filter((s) => oneLine(s.name).toLowerCase() === want);
	if (byName.length === 1 && byName[0]?.id) return { ok: true, id: byName[0].id };
	if (byName.length > 1) return { ok: false, error: `ambiguous series: ${ref} (${byName.map(seriesLabel).join(', ')})` };
	return { ok: false, error: `unknown series: ${ref}` };
}

// ── History ───────────────────────────────────────────────────

/** Appends a page of older digests, dropping ones already listed. */
export function mergeDigests(list: readonly Digest[], page: readonly Digest[]): Digest[] {
	const seen = new Set(list.map((d) => d.id));
	return [...list, ...page.filter((d) => !seen.has(d.id))];
}

/** The digest an older (dir 1) or newer (-1) step from the one with this ID reaches in the list (newest first). */
export function adjacentDigest(list: readonly Digest[], id: string, dir: 1 | -1): Digest | undefined {
	const i = list.findIndex((d) => d.id === id);
	if (i < 0) return undefined;
	return list[i + dir];
}

/** The ID of the digest the n-th input link (1-based) opens, or undefined. */
export function inputId(d: Digest | undefined, n: number): string | undefined {
	return d?.inputs?.[n - 1]?.id;
}

// ── Display ───────────────────────────────────────────────────

const pad = (n: number) => (n < 10 ? '0' + n : String(n));

/** "2026-10-05" in local time. */
function localDate(t: Date): string {
	return `${t.getFullYear()}-${pad(t.getMonth() + 1)}-${pad(t.getDate())}`;
}

/** "08:30" in local time. */
function localTime(t: Date): string {
	return `${pad(t.getHours())}:${pad(t.getMinutes())}`;
}

/** The end's date without what it shares with the start: "10-04" in the same year. */
function endDate(a: Date, b: Date): string {
	return a.getFullYear() === b.getFullYear() ? localDate(b).slice(5) : localDate(b);
}

/**
 * The period a digest covers, in local time like the feed's dates:
 * "2026-10-05" for a whole day, "2026-09-28 .. 10-04" for whole days, else
 * with the times ("2026-10-05 08:00 .. 12:30"). A period is whole days when
 * it starts at midnight and ends at the last second of a day (or at
 * midnight). The end leaves out the year (and the day) it shares with the
 * start.
 */
export function formatPeriod(start: string | undefined, end: string | undefined): string {
	const a = start ? new Date(start) : undefined;
	const b = end ? new Date(end) : undefined;
	if (!a || Number.isNaN(a.getTime()) || !b || Number.isNaN(b.getTime())) return '';
	const midnight = (t: Date) => t.getHours() === 0 && t.getMinutes() === 0 && t.getSeconds() === 0;
	const dayEnd = (t: Date) => t.getHours() === 23 && t.getMinutes() === 59 && t.getSeconds() === 59;
	const sameDay = localDate(a) === localDate(b);
	if (midnight(a) && (dayEnd(b) || midnight(b))) {
		return sameDay ? localDate(a) : `${localDate(a)} .. ${endDate(a, b)}`;
	}
	return `${localDate(a)} ${localTime(a)} .. ${sameDay ? '' : endDate(a, b) + ' '}${localTime(b)}`;
}

/** A time as its local date ("2026-10-05"), or "" when missing or unreadable. */
export function formatDay(ts: string | undefined): string {
	if (!ts) return '';
	const t = new Date(ts);
	return Number.isNaN(t.getTime()) ? '' : localDate(t);
}

/** The latest period end of a series as a date, or "" for an empty series. */
export function formatLatest(s: DigestSeries): string {
	return formatDay(s.latest_period_end);
}

// ── Deleting ──────────────────────────────────────────────────

const plural = (n: number, one: string, many = one + 's') => `${n} ${n === 1 ? one : many}`;

/** What deleting a series takes with it. */
export function deleteSeriesLines(s: DigestSeries): string[] {
	const n = s.digest_count ?? 0;
	return [n === 0 ? 'The series has no digests.' : `${plural(n, 'digest')} will be deleted.`, 'Use edit (e) instead to rename it.'];
}

/** How many series and digests an assessor has. */
export function assessorDigestUsage(series: readonly DigestSeries[], assessorId: string): { series: number; digests: number } {
	let n = 0;
	let digests = 0;
	for (const s of series) {
		if (s.assessor_id !== assessorId) continue;
		n++;
		digests += s.digest_count ?? 0;
	}
	return { series: n, digests };
}

/** The line the assessors page's delete confirmation adds, or "" when the assessor has no series. */
export function assessorDigestWarning(usage: { series: number; digests: number }): string {
	if (usage.series === 0) return '';
	return `It has ${plural(usage.series, 'digest series', 'digest series')}; ${plural(usage.digests, 'digest')} will be deleted with it.`;
}
