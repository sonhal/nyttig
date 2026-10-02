// Form state, validation and request bodies for the management views.
//
// Validation mirrors the daemon's (internal/server/service/validate.go) so
// mistakes show up next to the field before a round trip. The daemon stays
// authoritative: its error message is shown when it disagrees. Rule
// patterns are the exception: only their length is checked here, since Go
// RE2 syntax can't be checked with JavaScript's regex engine.

import type { RuleBody, SourceBody, TagBody, ViewBody } from './api';
import { sameFilter } from './filter';
import { format, parse } from './query';
import type { RuleField, SavedView, Source, Tag, TagRule } from './types';
import { filterToViewBody, viewToFilter } from './views';

// The daemon's limits.
export const MAX_NAME = 200;
export const MAX_ABBREVIATION = 16;
export const MAX_TAG_NAME = 64;
export const MAX_VIEW_NAME = 64;
export const MAX_TAG_PARENTS = 16;
export const MAX_URL_BYTES = 2048;
export const MAX_PATTERN_BYTES = 1024;
export const MIN_REFRESH_SEC = 60;
export const MAX_REFRESH_SEC = 7 * 24 * 60 * 60;
export const DEFAULT_REFRESH_SEC = 3600;

const INT32_MIN = -(2 ** 31);
const INT32_MAX = 2 ** 31 - 1;

const COLOR_RE = /^#[0-9A-Fa-f]{6}$/;
// Unicode Cc, as Go's unicode.IsControl.
const CONTROL_RE = /[\u0000-\u001F\u007F-\u009F]/;

/** Field name → message, for the fields that are invalid. */
export type Errors<T> = Partial<Record<keyof T, string>>;

export function hasErrors<T>(e: Errors<T>): boolean {
	return Object.keys(e).length > 0;
}

const utf8Len = (s: string) => new TextEncoder().encode(s).length;
const runeLen = (s: string) => [...s].length;

function nameError(label: string, v: string, max: number): string | undefined {
	if (v.trim() === '') return `${label} is required`;
	if (runeLen(v) > max) return `${label} must be at most ${max} characters`;
	if (CONTROL_RE.test(v)) return `${label} must not contain control characters`;
	return undefined;
}

function colorError(v: string): string | undefined {
	return v === '' || COLOR_RE.test(v) ? undefined : 'a hex color like #FF6600, or empty';
}

/** Checks a feed URL the way the daemon does: absolute http(s), a host, no credentials. */
export function urlError(v: string): string | undefined {
	if (v === '') return 'url is required';
	if (utf8Len(v) > MAX_URL_BYTES) return `url must be at most ${MAX_URL_BYTES} bytes`;
	let u: URL;
	try {
		u = new URL(v);
	} catch {
		return 'not a valid URL';
	}
	if (u.protocol !== 'http:' && u.protocol !== 'https:') return 'url must be http or https';
	if (!u.hostname) return 'url must include a host';
	if (u.username || u.password) return 'url must not contain credentials';
	return undefined;
}

// ── Refresh intervals ─────────────────────────────────────────

const UNITS: Record<string, number> = { '': 1, s: 1, m: 60, h: 3600, d: 86400 };

/**
 * Parses a refresh interval such as "90s", "30m", "1h", "2d" or "1h30m";
 * a bare number is seconds. Returns seconds, or null if malformed.
 */
export function parseInterval(input: string): number | null {
	const s = input.trim().toLowerCase().replace(/\s+/g, '');
	if (s === '') return null;
	if (/^\d+$/.test(s)) return Number(s);
	const re = /(\d+)([smhd])/gy;
	let total = 0;
	let end = 0;
	let m: RegExpExecArray | null;
	while ((m = re.exec(s)) !== null) {
		total += Number(m[1]) * (UNITS[m[2] ?? ''] ?? 1);
		end = re.lastIndex;
	}
	return end === s.length && end > 0 ? total : null;
}

/** Formats seconds as the shortest interval parseInterval reads back: "1h", "90m", "1h30m", "45s". */
export function formatInterval(sec: number | undefined): string {
	if (!sec || sec < 0) return '';
	let rest = Math.floor(sec);
	let out = '';
	for (const [unit, n] of [
		['d', 86400],
		['h', 3600],
		['m', 60]
	] as const) {
		if (rest >= n) {
			out += Math.floor(rest / n) + unit;
			rest %= n;
		}
	}
	if (rest > 0 || out === '') out += rest + 's';
	return out;
}

// ── Sources ───────────────────────────────────────────────────

export interface SourceForm {
	name: string;
	url: string;
	type: 'rss' | 'atom';
	/** A refresh interval as typed, e.g. "1h". */
	refresh: string;
	enabled: boolean;
	color: string;
	abbreviation: string;
}

/** The form for a new source (no argument) or for editing s. */
export function sourceForm(s?: Source): SourceForm {
	return {
		name: s?.name ?? '',
		url: s?.url ?? '',
		type: s?.type === 'atom' ? 'atom' : 'rss',
		refresh: formatInterval(s ? s.refresh_sec : DEFAULT_REFRESH_SEC),
		// New sources are enabled unless unchecked: proto3's default for
		// AddSourceRequest.enabled is false, so it is always sent.
		enabled: s ? !!s.enabled : true,
		color: s?.color ?? '',
		abbreviation: s?.abbreviation ?? ''
	};
}

export function validateSource(f: SourceForm): Errors<SourceForm> {
	const e: Errors<SourceForm> = {};
	const name = nameError('name', f.name, MAX_NAME);
	if (name) e.name = name;
	const url = urlError(f.url.trim());
	if (url) e.url = url;
	const sec = parseInterval(f.refresh);
	if (sec === null) e.refresh = 'an interval like 30m, 1h or 3600';
	else if (sec < MIN_REFRESH_SEC || sec > MAX_REFRESH_SEC) e.refresh = 'between 1m and 7d';
	const color = colorError(f.color.trim());
	if (color) e.color = color;
	const abbr = f.abbreviation.trim();
	if (abbr !== '') {
		const a = nameError('abbreviation', abbr, MAX_ABBREVIATION);
		if (a) e.abbreviation = a;
	}
	return e;
}

function sourceValues(f: SourceForm) {
	return {
		name: f.name.trim(),
		url: f.url.trim(),
		type: f.type,
		refresh_sec: parseInterval(f.refresh) ?? DEFAULT_REFRESH_SEC,
		enabled: f.enabled,
		color: f.color.trim(),
		abbreviation: f.abbreviation.trim()
	};
}

/** The POST /api/sources body for a valid form. */
export function sourceAddBody(f: SourceForm): SourceBody {
	const v = sourceValues(f);
	const body: SourceBody = {
		name: v.name,
		url: v.url,
		type: v.type,
		refresh_sec: v.refresh_sec,
		enabled: v.enabled
	};
	if (v.color) body.color = v.color;
	if (v.abbreviation) body.abbreviation = v.abbreviation;
	return body;
}

/**
 * The PATCH /api/sources/:id body for a valid form: only the fields that
 * differ from orig. Clearing color or abbreviation sends "".
 */
export function sourcePatchBody(orig: Source, f: SourceForm): SourceBody {
	const v = sourceValues(f);
	const p: SourceBody = {};
	if (v.name !== (orig.name ?? '')) p.name = v.name;
	if (v.url !== (orig.url ?? '')) p.url = v.url;
	if (v.type !== (orig.type || 'rss')) p.type = v.type;
	if (v.refresh_sec !== (orig.refresh_sec ?? 0)) p.refresh_sec = v.refresh_sec;
	if (v.enabled !== !!orig.enabled) p.enabled = v.enabled;
	if (v.color !== (orig.color ?? '')) p.color = v.color;
	if (v.abbreviation !== (orig.abbreviation ?? '')) p.abbreviation = v.abbreviation;
	return p;
}

// ── Tags ──────────────────────────────────────────────────────

export interface TagForm {
	name: string;
	color: string;
	/** IDs of the parent tags. */
	parent_ids: string[];
}

export function tagForm(t?: Tag): TagForm {
	return { name: t?.name ?? '', color: t?.color ?? '', parent_ids: [...(t?.parent_ids ?? [])] };
}

export function validateTag(f: TagForm): Errors<TagForm> {
	const e: Errors<TagForm> = {};
	const name = nameError('name', f.name, MAX_TAG_NAME);
	if (name) e.name = name;
	const color = colorError(f.color.trim());
	if (color) e.color = color;
	if (f.parent_ids.length > MAX_TAG_PARENTS) e.parent_ids = `at most ${MAX_TAG_PARENTS} parents`;
	return e;
}

export function tagAddBody(f: TagForm): TagBody {
	const body: TagBody = { name: f.name.trim() };
	if (f.color.trim()) body.color = f.color.trim();
	if (f.parent_ids.length > 0) body.parent_ids = [...f.parent_ids];
	return body;
}

export function tagPatchBody(orig: Tag, f: TagForm): TagBody {
	const p: TagBody = {};
	if (f.name.trim() !== (orig.name ?? '')) p.name = f.name.trim();
	if (f.color.trim() !== (orig.color ?? '')) p.color = f.color.trim();
	const was = [...(orig.parent_ids ?? [])].sort();
	const now = [...f.parent_ids].sort();
	if (was.length !== now.length || was.some((id, i) => id !== now[i])) p.parent_ids = [...f.parent_ids];
	return p;
}

// ── Views ─────────────────────────────────────────────────────

export interface ViewForm {
	name: string;
	/** The filter as the "/" bar's query text (query.ts). */
	query: string;
	favorite: boolean;
}

/** The form for a new view (no argument; favorite, like ":save <name>") or for editing v. */
export function viewForm(v?: SavedView, sources: readonly Source[] = [], tags: readonly Tag[] = []): ViewForm {
	return {
		name: v?.name ?? '',
		query: v ? format(viewToFilter(v), sources, tags) : '',
		favorite: v ? !!v.favorite : true
	};
}

/** The query is checked with the "/" bar's parser, so it shows the same errors. */
export function validateView(f: ViewForm, sources: readonly Source[], tags: readonly Tag[]): Errors<ViewForm> {
	const e: Errors<ViewForm> = {};
	const name = nameError('name', f.name, MAX_VIEW_NAME);
	if (name) e.name = name;
	const q = parse(f.query, sources, tags).errors[0];
	if (q) e.query = q.message;
	return e;
}

/** The POST /api/views body for a valid form. */
export function viewAddBody(f: ViewForm, sources: readonly Source[], tags: readonly Tag[]): ViewBody {
	return {
		name: f.name.trim(),
		filter: filterToViewBody(parse(f.query, sources, tags).filter),
		favorite: f.favorite
	};
}

/** The PATCH /api/views/:id body: only what changed; a changed query sends the whole filter. */
export function viewPatchBody(orig: SavedView, f: ViewForm, sources: readonly Source[], tags: readonly Tag[]): ViewBody {
	const p: ViewBody = {};
	if (f.name.trim() !== (orig.name ?? '')) p.name = f.name.trim();
	const filter = parse(f.query, sources, tags).filter;
	if (!sameFilter(viewToFilter(orig), filter)) p.filter = filterToViewBody(filter);
	if (f.favorite !== !!orig.favorite) p.favorite = f.favorite;
	return p;
}

// ── Rules ─────────────────────────────────────────────────────

export interface RuleForm {
	tag_id: string;
	/** "" = global (all sources). */
	source_id: string;
	field: RuleField;
	pattern: string;
	priority: string;
}

export function ruleField(v: string | undefined): RuleField {
	return v === 'title' || v === 'description' ? v : 'both';
}

export function ruleForm(r?: TagRule, defaultTagId = ''): RuleForm {
	return {
		tag_id: r?.tag_id ?? defaultTagId,
		source_id: r?.source_id && r.source_id !== '0' ? r.source_id : '',
		field: ruleField(r?.field),
		pattern: r?.pattern ?? '',
		priority: String(r?.priority ?? 0)
	};
}

/** Pattern checks that don't depend on regex syntax. */
export function patternError(p: string): string | undefined {
	if (p === '') return 'pattern is required';
	if (utf8Len(p) > MAX_PATTERN_BYTES) return `pattern must be at most ${MAX_PATTERN_BYTES} bytes`;
	return undefined;
}

export function validateRule(f: RuleForm): Errors<RuleForm> {
	const e: Errors<RuleForm> = {};
	if (!f.tag_id) e.tag_id = 'pick a tag';
	const p = patternError(f.pattern);
	if (p) e.pattern = p;
	const prio = f.priority.trim();
	if (!/^-?\d+$/.test(prio) || Number(prio) < INT32_MIN || Number(prio) > INT32_MAX) {
		e.priority = 'a whole number';
	}
	return e;
}

/**
 * The POST /api/rules body. The pattern is sent exactly as typed: leading
 * or trailing spaces are part of a regular expression.
 */
export function ruleBody(f: RuleForm): RuleBody {
	const body: RuleBody = { tag_id: f.tag_id, field: f.field, pattern: f.pattern };
	if (f.source_id) body.source_id = f.source_id;
	const prio = Number(f.priority.trim());
	if (prio) body.priority = prio;
	return body;
}

/** Whether the form describes rule r exactly (so an edit would change nothing). */
export function sameRule(r: TagRule, f: RuleForm): boolean {
	const o = ruleForm(r);
	return (
		o.tag_id === f.tag_id &&
		o.source_id === f.source_id &&
		o.field === f.field &&
		o.pattern === f.pattern &&
		Number(o.priority) === Number(f.priority.trim())
	);
}
