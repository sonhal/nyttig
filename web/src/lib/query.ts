// The query syntax of the "/" bar:
//
//   kernel panic tag:rust src:"Hacker News" is:unviewed sort:oldest
//   tag:CVE score:claude>=0.7 sort:score
//
// "tag:", "src:", "is:unviewed" and "sort:newest|oldest|score" set the
// filter, "score:<assessor>" picks the assessor whose scores to show first
// (">=0.7" keeps only items it scored that high, and "sort:score" orders by
// its scores, so it needs a "score:" term) and "unassessed:<assessor>" keeps
// items it has not assessed. Every other word is free text (the FTS search, matched as a phrase). A
// name with spaces is quoted; inside quotes \" and \\ are escapes. A
// quoted token is always free text or a literal name, so "tag:x" searches
// for that text, and tag:"#1" is a tag called #1. An unquoted #<id> is an ID,
// which is how a filter whose name is unknown (metadata not loaded yet, or
// a name two tags share) is written.
//
// parse() and format() are inverses: format(parse(text).filter) is the
// canonical spelling of text, and parse(format(f)) gives f back. Names are
// only known to the client, so both take the current sources and tags.
// Tag rule patterns never come into this: the query only ever names things.

import { defaultFilter, parseScore } from './filter';
import { oneLine } from './sanitize';
import type { Assessor, Filter, Sort, Source, Tag } from './types';

export interface QueryError {
	/** The offending token's range in the text, for highlighting. */
	start: number;
	end: number;
	message: string;
}

export interface ParseResult {
	/** The filter the valid parts describe; unknown names are left out. */
	filter: Filter;
	/** Why the text cannot be applied; empty when it can. */
	errors: QueryError[];
}

const ID_RE = /^[1-9][0-9]{0,18}$/;
export const MAX_QUERY = 500;

type Key = 'tag' | 'src' | 'is' | 'sort' | 'score' | 'unassessed';
const KEY_ALIASES: Record<string, Key> = {
	tag: 'tag',
	src: 'src',
	source: 'src',
	is: 'is',
	sort: 'sort',
	score: 'score',
	unassessed: 'unassessed'
};

/** The operators, for the help overlay. */
export const QUERY_KEYS: readonly { usage: string; desc: string }[] = [
	{ usage: 'tag:<name>', desc: 'only items with this tag or a child tag (tag:rust, tag:"Release notes")' },
	{ usage: 'src:<name>', desc: 'only this source, by name or abbreviation' },
	{ usage: 'is:unviewed', desc: 'only unviewed items' },
	{ usage: 'score:<assessor>[>=0.7]', desc: "show an assessor's scores first; with >= only items it scored at least that" },
	{ usage: 'unassessed:<assessor>', desc: 'only items this assessor has not assessed (an assessor finds its work this way)' },
	{ usage: 'sort:newest|oldest|score', desc: 'the sort order; score needs a score: term' },
	{ usage: '<words>', desc: 'full-text search, matched as a phrase ("quoted" to stop a word being read as an operator)' }
];

const IS_VALUES = ['unviewed'];
const SORT_VALUES: Sort[] = ['newest', 'oldest', 'score'];

// ── Tokens ────────────────────────────────────────────────────

interface Token {
	start: number;
	end: number;
	/** The operator, or undefined for free text. */
	key?: Key;
	/** The operator as typed (for messages). */
	keyText?: string;
	/** The value (unquoted), or the free text. */
	value: string;
	quoted: boolean;
	/** Offset of the value in the text, for completion. */
	valueStart: number;
	/** score: the text after ">=", when there is one. */
	min?: string;
}

const isSpace = (c: string) => /\s/.test(c);

/** Reads a quoted string starting at the opening quote at i. */
function readQuoted(text: string, i: number): { value: string; end: number } {
	let out = '';
	let j = i + 1;
	while (j < text.length) {
		const c = text[j]!;
		const next = text[j + 1];
		if (c === '\\' && (next === '"' || next === '\\')) {
			out += next;
			j += 2;
			continue;
		}
		if (c === '"') return { value: out, end: j + 1 };
		out += c;
		j++;
	}
	// Unterminated: the quote runs to the end, which is what you want
	// while still typing the closing one.
	return { value: out, end: text.length };
}

function tokenize(text: string): Token[] {
	const tokens: Token[] = [];
	const keyRe = /([A-Za-z]+):/y;
	let i = 0;
	while (i < text.length) {
		if (isSpace(text[i]!)) {
			i++;
			continue;
		}
		const start = i;
		let key: Key | undefined;
		let keyText: string | undefined;
		keyRe.lastIndex = i;
		const m = keyRe.exec(text);
		const k = m ? KEY_ALIASES[m[1]!.toLowerCase()] : undefined;
		if (m && k) {
			key = k;
			keyText = m[1]!;
			i += m[0].length;
		}
		const valueStart = i;
		let value: string;
		let quoted = false;
		let min: string | undefined;
		if (text[i] === '"') {
			const q = readQuoted(text, i);
			value = q.value;
			quoted = true;
			i = q.end;
			if (key === 'score' && text.startsWith('>=', i)) {
				const from = i + 2;
				i = from;
				while (i < text.length && !isSpace(text[i]!)) i++;
				min = text.slice(from, i);
			}
		} else {
			while (i < text.length && !isSpace(text[i]!)) i++;
			value = text.slice(valueStart, i);
			const ge = key === 'score' ? value.indexOf('>=') : -1;
			if (ge >= 0) {
				min = value.slice(ge + 2);
				value = value.slice(0, ge);
			}
		}
		tokens.push({ start, end: i, key, keyText, value, quoted, valueStart, min });
	}
	return tokens;
}

// ── Names ─────────────────────────────────────────────────────

interface Named {
	id?: string;
	name?: string;
	abbreviation?: string;
}

/**
 * The ID of the entry a name means: an exact name, then a name ignoring
 * case, then (sources) an abbreviation ignoring case. An unquoted #<id> is
 * an ID as it stands. Returns undefined when nothing matches.
 */
function resolve(list: readonly Named[], value: string, quoted: boolean): string | undefined {
	if (!quoted && value.startsWith('#')) {
		const id = value.slice(1);
		return ID_RE.test(id) ? id : undefined;
	}
	const named = list.filter((x): x is Named & { id: string } => !!x.id);
	const want = oneLine(value);
	const lower = want.toLowerCase();
	return (
		named.find((x) => oneLine(x.name) === want)?.id ??
		named.find((x) => oneLine(x.name).toLowerCase() === lower)?.id ??
		named.find((x) => !!x.abbreviation && oneLine(x.abbreviation).toLowerCase() === lower)?.id
	);
}

export const findSource = (sources: readonly Source[], name: string, quoted = true) =>
	resolve(sources, name, quoted);
export const findTag = (tags: readonly Tag[], name: string, quoted = true) => resolve(tags, name, quoted);
export const findAssessor = (assessors: readonly Assessor[], name: string, quoted = true) =>
	resolve(assessors, name, quoted);

// ── Parse ─────────────────────────────────────────────────────

export function parse(
	text: string,
	sources: readonly Source[],
	tags: readonly Tag[],
	assessors: readonly Assessor[] = []
): ParseResult {
	const filter: Filter = { ...defaultFilter };
	const errors: QueryError[] = [];
	const words: string[] = [];
	const err = (t: Token, message: string) => errors.push({ start: t.start, end: t.end, message });

	let sortSet = false;
	let sortToken: Token | undefined;

	for (const t of tokenize(text)) {
		switch (t.key) {
			case undefined:
				words.push(...t.value.split(/\s+/).filter(Boolean));
				break;
			case 'tag':
			case 'src': {
				const isTag = t.key === 'tag';
				const what = isTag ? 'tag' : 'source';
				if (!t.value) {
					err(t, `${t.keyText}: needs a ${what} name`);
					break;
				}
				const id = isTag ? findTag(tags, t.value, t.quoted) : findSource(sources, t.value, t.quoted);
				if (id === undefined) {
					err(t, t.value.startsWith('#') && !t.quoted ? `bad ${what} id: ${t.value}` : `unknown ${what}: ${t.value}`);
					break;
				}
				const prev = isTag ? filter.tag : filter.source;
				if (prev && prev !== id) {
					err(t, `only one ${what} at a time`);
					break;
				}
				if (isTag) filter.tag = id;
				else filter.source = id;
				break;
			}
			case 'score':
			case 'unassessed': {
				const isScore = t.key === 'score';
				if (!t.value) {
					err(t, `${t.keyText}: needs an assessor name`);
					break;
				}
				const id = findAssessor(assessors, t.value, t.quoted);
				if (id === undefined) {
					err(t, t.value.startsWith('#') && !t.quoted ? `bad assessor id: ${t.value}` : `unknown assessor: ${t.value}`);
					break;
				}
				const prev = isScore ? filter.assessor : filter.unassessed;
				if (prev && prev !== id) {
					err(t, isScore ? 'only one score: assessor at a time' : 'only one unassessed: assessor at a time');
					break;
				}
				if (!isScore) {
					filter.unassessed = id;
					break;
				}
				let min: number | null = null;
				if (t.min !== undefined) {
					min = parseScore(t.min);
					if (min === null) {
						err(t, `score: the minimum must be a number from 0 to 1, not ${t.min || 'nothing'}`);
						break;
					}
				}
				filter.assessor = id;
				if (min !== null) filter.minScore = min;
				break;
			}
			case 'is':
				if (t.value.toLowerCase() === 'unviewed') filter.unviewed = true;
				else err(t, t.value ? `unknown is: value: ${t.value} (try unviewed)` : 'is: needs a value (unviewed)');
				break;
			case 'sort': {
				const v = t.value.toLowerCase();
				const s = SORT_VALUES.find((x) => x === v);
				if (!s) {
					err(t, t.value ? `unknown sort: ${t.value} (newest, oldest or score)` : 'sort: needs newest, oldest or score');
				} else if (sortSet && filter.sort !== s) {
					err(t, 'sort given twice');
				} else {
					filter.sort = s;
					sortSet = true;
					sortToken = t;
				}
				break;
			}
		}
	}

	// The score sort orders by one assessor's scores, so it names one.
	if (filter.sort === 'score' && !filter.assessor && sortToken) {
		err(sortToken, 'sort:score needs a score:<assessor> term');
	}

	filter.q = words.join(' ');
	if (filter.q.length > MAX_QUERY) {
		errors.push({ start: 0, end: text.length, message: `the search text is too long (${MAX_QUERY} characters at most)` });
		filter.q = filter.q.slice(0, MAX_QUERY);
	}
	return { filter, errors };
}

// ── Format ────────────────────────────────────────────────────

const OPERATOR_PREFIX = /^[A-Za-z]+:/;

function quote(s: string): string {
	return '"' + s.replace(/["\\]/g, '\\$&') + '"';
}

/** A name as a value: bare when that parses back to the same name. */
function nameValue(name: string): string {
	return name === '' || /[\s"\\]/.test(name) || name.startsWith('#') ? quote(name) : name;
}

/** A free-text word, quoted when it would be read as something else. */
function word(w: string): string {
	const operator = OPERATOR_PREFIX.test(w) && KEY_ALIASES[w.slice(0, w.indexOf(':')).toLowerCase()] !== undefined;
	return operator || w.startsWith('"') ? quote(w) : w;
}

function named(list: readonly Named[], id: string, beforeMin = false): string {
	const entry = list.find((x) => x.id === id);
	const name = oneLine(entry?.name);
	// The ID when there is no name, or when the name would mean another entry.
	if (!name || resolve(list, name, true) !== id) return '#' + id;
	// A name written before ">=" must not contain it, so it is quoted.
	return beforeMin && name.includes('>') ? quote(name) : nameValue(name);
}

/** The canonical text of a filter, which parse() turns back into it. */
export function format(
	f: Filter,
	sources: readonly Source[],
	tags: readonly Tag[],
	assessors: readonly Assessor[] = []
): string {
	const parts: string[] = [];
	const words = f.q.split(/\s+/).filter(Boolean);
	parts.push(...words.map(word));
	if (f.source) parts.push('src:' + named(sources, f.source));
	if (f.tag) parts.push('tag:' + named(tags, f.tag));
	if (f.unviewed) parts.push('is:unviewed');
	if (f.assessor) {
		parts.push('score:' + named(assessors, f.assessor, true) + (f.minScore !== null ? '>=' + f.minScore : ''));
	}
	if (f.unassessed) parts.push('unassessed:' + named(assessors, f.unassessed));
	if (f.sort !== defaultFilter.sort) parts.push('sort:' + f.sort);
	return parts.join(' ');
}

// ── Completion ────────────────────────────────────────────────

export interface Candidate {
	/** What the list shows. */
	label: string;
	/** What replaces the token, e.g. `tag:"Hacker News"`. */
	insert: string;
	kind: 'tag' | 'src' | 'is' | 'sort' | 'score' | 'unassessed';
	color?: string;
}

export interface Completion {
	/** The token being completed. */
	start: number;
	end: number;
	candidates: Candidate[];
}

const MAX_CANDIDATES = 8;

/**
 * Candidates for the operator value under the caret (tag:ru → tag:rust),
 * best first: names that start with what was typed, then names that
 * contain it. Null when the caret is not in an operator's value.
 */
export function complete(
	text: string,
	caret: number,
	sources: readonly Source[],
	tags: readonly Tag[],
	assessors: readonly Assessor[] = []
): Completion | null {
	const t = tokenize(text).find((x) => x.key && x.valueStart <= caret && caret <= x.end);
	// With a minimum typed, the name is done.
	if (!t?.key || t.min !== undefined) return null;
	const typed = t.value.slice(0, Math.max(0, caret - t.valueStart - (t.quoted ? 1 : 0))).toLowerCase();

	const key = t.key;
	let entries: { label: string; color?: string }[];
	switch (key) {
		case 'tag':
			entries = tags.filter((x) => x.name).map((x) => ({ label: oneLine(x.name), color: x.color }));
			break;
		case 'src':
			entries = sources.filter((x) => x.name).map((x) => ({ label: oneLine(x.name), color: x.color }));
			break;
		case 'score':
		case 'unassessed':
			entries = assessors.filter((x) => x.name).map((x) => ({ label: oneLine(x.name), color: x.color }));
			break;
		case 'is':
			entries = IS_VALUES.map((label) => ({ label }));
			break;
		case 'sort':
			entries = SORT_VALUES.map((label) => ({ label }));
			break;
	}
	const starts = entries.filter((e) => e.label.toLowerCase().startsWith(typed));
	const contains = entries.filter((e) => !e.label.toLowerCase().startsWith(typed) && e.label.toLowerCase().includes(typed));
	const candidates = [...starts, ...contains].slice(0, MAX_CANDIDATES).map(
		(e): Candidate => ({
			label: e.label,
			insert: `${key}:${key === 'is' || key === 'sort' ? e.label : key === 'score' && e.label.includes('>') ? quote(e.label) : nameValue(e.label)}`,
			kind: key,
			color: e.color
		})
	);
	// Already complete: nothing to offer.
	if (candidates.length === 1 && candidates[0]!.insert === text.slice(t.start, t.end)) return null;
	return { start: t.start, end: t.end, candidates };
}

/** The text with the token replaced by c, and where the caret goes. */
export function applyCompletion(text: string, c: Completion, cand: Candidate): { text: string; caret: number } {
	const before = text.slice(0, c.start);
	const after = text.slice(c.end);
	// A space after the name, to carry on typing; the caret goes past it.
	const gap = after === '' || !isSpace(after[0]!) ? ' ' : '';
	return { text: before + cand.insert + gap + after, caret: before.length + cand.insert.length + 1 };
}
