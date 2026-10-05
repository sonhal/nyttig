// The ":" command line. Pages: ":feed", ":sources", ":tags", ":rules",
// ":views". Filter shortcuts: ":sort", ":unviewed", ":src", ":tag". Saved
// views: ":view <name>|all" opens one, ":save [name]" saves the current
// filter. Assessments: ":score <assessor>|all [min]" and ":unassessed
// <assessor>|all" filter, ":assessors" is the management page. Digests:
// ":digests" is the reading page and ":digest <assessor>/<series>" opens one
// series on it. Also ":refresh", ":time", ":follow" and ":help". A unique prefix is enough (":sor", ":un"); a few
// short forms are fixed so they keep the meaning they had before the
// longer commands existed (":s" sources, ":r" rules, ":f" feed, ":t" tags,
// ":q" feed).
//
// parseCommand is pure: names are resolved against the sources and tags it
// is given, so the result carries IDs and the caller only has to act.

import { parseScore, withAssessor } from './filter';
import { parseRate } from './rate';
import { findSeries, seriesNames } from './digests';
import { findAssessor, findSource, findTag } from './query';
import type { Assessor, DigestSeries, Filter, SavedView, Sort, Source, Tag } from './types';
import { findView } from './views';

export type Page = 'feed' | 'sources' | 'tags' | 'rules' | 'views' | 'assessors' | 'digests';

export const PAGES: readonly Page[] = ['feed', 'sources', 'tags', 'rules', 'views', 'assessors', 'digests'];

/** The route of each page. */
export const PAGE_PATHS: Record<Page, string> = {
	feed: '/',
	sources: '/sources',
	tags: '/tags',
	rules: '/rules',
	views: '/views',
	assessors: '/assessors',
	digests: '/digests'
};

export type TimeMode = 'relative' | 'absolute';

export type Command =
	| { type: 'page'; page: Page }
	/** Opens a digest series (by ID) on the digests page. */
	| { type: 'digest'; series: string }
	/** Opens a saved view; id "" is the unfiltered feed. */
	| { type: 'view'; id: string }
	/** Saves the current filter: into the active view when name is "", else as a new view. */
	| { type: 'save'; name: string }
	| { type: 'sort'; sort: Sort | 'toggle' }
	| { type: 'unviewed'; value: boolean | 'toggle' }
	/** id "" is all sources. */
	| { type: 'source'; id: string }
	| { type: 'tag'; id: string }
	/** The assessor whose scores to show and use ("" is none, which also drops the minimum); min is a minimum score or null. */
	| { type: 'score'; id: string; min: number | null }
	/** Only items this assessor has not assessed ("" is off). */
	| { type: 'unassessed'; id: string }
	/** Rates the selected item yourself, as the assessor "me". */
	| { type: 'rate'; score: number; note: string }
	/** id "" is every source. */
	| { type: 'refresh'; source: string }
	| { type: 'time'; mode: TimeMode | 'toggle' }
	| { type: 'follow' }
	| { type: 'help' };

export interface CommandContext {
	sources: readonly Source[];
	tags: readonly Tag[];
	/** The saved views; left out, there are none. */
	views?: readonly SavedView[];
	/** The assessors; left out, there are none. */
	assessors?: readonly Assessor[];
	/** The digest series; left out, there are none. */
	series?: readonly DigestSeries[];
}

export type CommandResult = { ok: true; cmd: Command } | { ok: false; error: string };

interface Spec {
	name: string;
	args?: string;
	desc: string;
}

/** Every command, in help order. The help overlay lists this table. */
export const COMMANDS: readonly Spec[] = [
	{ name: 'feed', desc: 'the feed (:q too)' },
	{ name: 'sources', desc: 'manage sources' },
	{ name: 'tags', desc: 'manage tags' },
	{ name: 'rules', desc: 'manage tag rules' },
	{ name: 'views', desc: 'manage saved views' },
	{ name: 'assessors', desc: 'manage assessors' },
	{ name: 'digests', desc: 'read digests (:d too)' },
	{ name: 'digest', args: '<assessor>/<series>', desc: 'open a digest series' },
	{ name: 'sort', args: '[newest|oldest|score]', desc: 'set the sort order (score needs an assessor), or toggle it' },
	{ name: 'unviewed', args: '[on|off]', desc: 'show only unviewed items, or toggle it' },
	{ name: 'src', args: '<name>|all', desc: 'filter by source (:src alone lists sources)' },
	{ name: 'tag', args: '<name>|all', desc: 'filter by tag' },
	{ name: 'view', args: '<name>|all', desc: 'open a saved view, or the unfiltered feed' },
	{ name: 'save', args: '[name]', desc: 'save the filter into the active view, or as a new favorite view' },
	{ name: 'score', args: '<assessor>|all [min]', desc: "show an assessor's scores, optionally only those of at least min" },
	{ name: 'unassessed', args: '<assessor>|all', desc: 'only items this assessor has not assessed' },
	{ name: 'rate', args: '<score> [note]', desc: 'rate the selected item yourself (assessor "me", score 0 to 1)' },
	{ name: 'refresh', args: '[source]', desc: 'fetch all sources now, or one' },
	{ name: 'time', args: '[relative|absolute]', desc: 'how times are shown, or toggle it' },
	{ name: 'follow', desc: 'jump to the newest and follow' },
	{ name: 'help', desc: 'the key list' }
];

const NAMES = COMMANDS.map((c) => c.name);

/** Short forms that win over prefix matching. */
const ALIASES: Record<string, string> = {
	q: 'feed',
	quit: 'feed',
	f: 'feed',
	s: 'sources',
	so: 'sources',
	r: 'rules',
	t: 'tags',
	ta: 'tags',
	u: 'unviewed',
	un: 'unviewed',
	v: 'view',
	d: 'digests',
	h: 'help',
	'?': 'help'
};

function resolveName(word: string): { name: string } | { error: string } {
	const alias = ALIASES[word];
	if (alias) return { name: alias };
	if (NAMES.includes(word)) return { name: word };
	const matches = NAMES.filter((n) => n.startsWith(word));
	const only = matches.length === 1 ? matches[0] : undefined;
	if (only) return { name: only };
	if (matches.length > 1) return { error: `ambiguous: ${word} (${matches.join(', ')})` };
	return { error: `unknown command: ${word} (try ${NAMES.join(', ')})` };
}

function unquote(s: string): string {
	return s.length >= 2 && s.startsWith('"') && s.endsWith('"') ? s.slice(1, -1) : s;
}

const ALL = new Set(['all', '*', '-']);

function oneOf<T extends string>(arg: string, values: readonly T[]): T | undefined {
	const a = arg.toLowerCase();
	return values.find((v) => v === a);
}

export function parseCommand(input: string, ctx: CommandContext = { sources: [], tags: [] }): CommandResult {
	const text = input.trim().replace(/^:+/, '').trim();
	if (text === '') return { ok: false, error: 'commands: ' + NAMES.join(' ') };
	const space = text.search(/\s/);
	const head = (space < 0 ? text : text.slice(0, space)).toLowerCase();
	const arg = space < 0 ? '' : text.slice(space).trim();

	const r = resolveName(head);
	if ('error' in r) return { ok: false, error: r.error };
	const fail = (error: string): CommandResult => ({ ok: false, error });
	const ok = (cmd: Command): CommandResult => ({ ok: true, cmd });

	switch (r.name) {
		case 'feed':
		case 'sources':
		case 'tags':
		case 'rules':
		case 'views':
		case 'assessors':
		case 'digests':
			return arg ? fail(`${r.name} takes no argument`) : ok({ type: 'page', page: r.name });
		case 'digest': {
			if (!arg) return fail('digest: <assessor>/<series>');
			const found = findSeries(ctx.series ?? [], unquote(arg));
			return found.ok ? ok({ type: 'digest', series: found.id }) : fail(found.error);
		}
		case 'view': {
			if (!arg) return fail('view: a view name, or all');
			const name = unquote(arg);
			const views = ctx.views ?? [];
			if (ALL.has(name.toLowerCase()) && findView(views, name) === undefined) return ok({ type: 'view', id: '' });
			const v = findView(views, name);
			return v?.id === undefined ? fail(`unknown view: ${name}`) : ok({ type: 'view', id: v.id });
		}
		case 'save':
			return ok({ type: 'save', name: unquote(arg).trim() });
		case 'help':
			return arg ? fail('help takes no argument') : ok({ type: 'help' });
		case 'follow':
			return arg ? fail('follow takes no argument') : ok({ type: 'follow' });
		case 'sort': {
			if (!arg) return ok({ type: 'sort', sort: 'toggle' });
			const s = oneOf(arg, ['newest', 'oldest', 'score'] as const);
			return s ? ok({ type: 'sort', sort: s }) : fail(`sort: newest, oldest or score, not ${arg}`);
		}
		case 'unviewed': {
			if (!arg) return ok({ type: 'unviewed', value: 'toggle' });
			const v = oneOf(arg, ['on', 'off'] as const);
			return v ? ok({ type: 'unviewed', value: v === 'on' }) : fail(`unviewed: on or off, not ${arg}`);
		}
		case 'time': {
			if (!arg) return ok({ type: 'time', mode: 'toggle' });
			const m = oneOf(arg, ['relative', 'absolute'] as const);
			return m ? ok({ type: 'time', mode: m }) : fail(`time: relative or absolute, not ${arg}`);
		}
		case 'src': {
			// Before the filter shortcut existed, ":src" was a short form of
			// ":sources"; it still is when it has no argument.
			if (!arg) return ok({ type: 'page', page: 'sources' });
			const name = unquote(arg);
			if (ALL.has(name.toLowerCase()) && findSource(ctx.sources, name) === undefined) return ok({ type: 'source', id: '' });
			const id = findSource(ctx.sources, name, false);
			return id === undefined ? fail(`unknown source: ${name}`) : ok({ type: 'source', id });
		}
		case 'tag': {
			if (!arg) return fail('tag: a tag name, or all');
			const name = unquote(arg);
			if (ALL.has(name.toLowerCase()) && findTag(ctx.tags, name) === undefined) return ok({ type: 'tag', id: '' });
			const id = findTag(ctx.tags, name, false);
			return id === undefined ? fail(`unknown tag: ${name}`) : ok({ type: 'tag', id });
		}
		case 'score': {
			if (!arg) return fail('score: an assessor name, or all');
			const assessors = ctx.assessors ?? [];
			const name = unquote(arg);
			if (ALL.has(name.toLowerCase()) && findAssessor(assessors, name) === undefined) return ok({ type: 'score', id: '', min: null });
			// The whole text is a name first ("gpt 4"); otherwise a trailing number is the minimum.
			const whole = findAssessor(assessors, name, false);
			if (whole !== undefined) return ok({ type: 'score', id: whole, min: null });
			const m = /^(.*?)\s+(?:>=\s*)?(\d*\.?\d+)$/.exec(arg);
			if (m) {
				const id = findAssessor(assessors, unquote(m[1]!.trim()), false);
				const min = parseScore(m[2]);
				if (id === undefined) return fail(`unknown assessor: ${unquote(m[1]!.trim())}`);
				return min === null ? fail(`score: the minimum is a number from 0 to 1, not ${m[2]}`) : ok({ type: 'score', id, min });
			}
			return fail(`unknown assessor: ${name}`);
		}
		case 'unassessed': {
			if (!arg) return fail('unassessed: an assessor name, or all');
			const assessors = ctx.assessors ?? [];
			const name = unquote(arg);
			if (ALL.has(name.toLowerCase()) && findAssessor(assessors, name) === undefined) return ok({ type: 'unassessed', id: '' });
			const id = findAssessor(assessors, name, false);
			return id === undefined ? fail(`unknown assessor: ${name}`) : ok({ type: 'unassessed', id });
		}
		case 'rate': {
			const r = parseRate(arg);
			return r.ok ? ok({ type: 'rate', score: r.score, note: r.note }) : fail(r.error);
		}
		case 'refresh': {
			if (!arg) return ok({ type: 'refresh', source: '' });
			const name = unquote(arg);
			const id = findSource(ctx.sources, name, false);
			return id === undefined ? fail(`unknown source: ${name}`) : ok({ type: 'refresh', source: id });
		}
	}
	return fail(`unknown command: ${head}`);
}

/** The filter after a filter command, or null for any other command. */
export function applyToFilter(cmd: Command, f: Filter): Filter | null {
	switch (cmd.type) {
		case 'sort':
			return { ...f, sort: cmd.sort === 'toggle' ? (f.sort === 'newest' ? 'oldest' : 'newest') : cmd.sort };
		case 'score':
			return { ...withAssessor(f, cmd.id), minScore: cmd.id ? cmd.min : null };
		case 'unassessed':
			return { ...f, unassessed: cmd.id };
		case 'unviewed':
			return { ...f, unviewed: cmd.value === 'toggle' ? !f.unviewed : cmd.value };
		case 'source':
			return { ...f, source: cmd.id };
		case 'tag':
			return { ...f, tag: cmd.id };
	}
	return null;
}

// ── Completion ────────────────────────────────────────────────

export interface CommandCompletion {
	/** Where in the input the completed word starts. */
	from: number;
	/** Whole-word candidates, best first. */
	candidates: string[];
}

function startsWith(list: readonly string[], prefix: string): string[] {
	const p = prefix.toLowerCase();
	return list.filter((x) => x.toLowerCase().startsWith(p));
}

/**
 * Candidates for the word at the end of the input: command names, then
 * the argument of ":sort", ":src" and the others. Names are returned as
 * they should be typed (":src Hacker News" needs no quotes: the argument
 * is the rest of the line).
 */
export function completeCommand(input: string, ctx: CommandContext): CommandCompletion {
	const text = input.replace(/^:+/, '');
	const lead = input.length - text.length;
	const space = text.search(/\s/);
	if (space < 0) return { from: lead, candidates: startsWith(NAMES, text) };

	const r = resolveName(text.slice(0, space).toLowerCase());
	if ('error' in r) return { from: input.length, candidates: [] };
	const rest = text.slice(space);
	const argStart = lead + space + (rest.length - rest.trimStart().length);
	const typed = unquote(input.slice(argStart));
	const names = (xs: readonly { name?: string }[]) => xs.flatMap((x) => (x.name ? [x.name.replace(/\s+/g, ' ')] : []));
	let list: readonly string[] = [];
	switch (r.name) {
		case 'sort':
			list = ['newest', 'oldest', 'score'];
			break;
		case 'score':
		case 'unassessed':
			list = [...names(ctx.assessors ?? []), 'all'];
			break;
		case 'unviewed':
			list = ['on', 'off'];
			break;
		case 'time':
			list = ['relative', 'absolute'];
			break;
		case 'src':
			list = [...names(ctx.sources), 'all'];
			break;
		case 'tag':
			list = [...names(ctx.tags), 'all'];
			break;
		case 'refresh':
			list = names(ctx.sources);
			break;
		case 'view':
			list = [...names(ctx.views ?? []), 'all'];
			break;
		case 'digest':
			list = seriesNames(ctx.series ?? []);
			break;
	}
	return { from: argStart, candidates: startsWith(list, typed) };
}

/** The longest prefix all words share, ignoring case (the first word's spelling). */
export function commonPrefix(words: readonly string[]): string {
	const first = words[0];
	if (first === undefined) return '';
	let n = first.length;
	for (const w of words) {
		let i = 0;
		while (i < n && i < w.length && w[i]!.toLowerCase() === first[i]!.toLowerCase()) i++;
		n = i;
	}
	return first.slice(0, n);
}
