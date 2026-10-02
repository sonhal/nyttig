// The ":" command line. Pages: ":feed", ":sources", ":tags", ":rules". Filter
// shortcuts: ":sort", ":unviewed", ":src", ":tag". Also ":refresh", ":time",
// ":follow" and ":help". A unique prefix is enough (":sor", ":un"); a few
// short forms are fixed so they keep the meaning they had before the
// longer commands existed (":s" sources, ":r" rules, ":f" feed, ":t" tags,
// ":q" feed).
//
// parseCommand is pure: names are resolved against the sources and tags it
// is given, so the result carries IDs and the caller only has to act.

import { findSource, findTag } from './query';
import type { Filter, Sort, Source, Tag } from './types';

export type Page = 'feed' | 'sources' | 'tags' | 'rules';

export const PAGES: readonly Page[] = ['feed', 'sources', 'tags', 'rules'];

/** The route of each page. */
export const PAGE_PATHS: Record<Page, string> = {
	feed: '/',
	sources: '/sources',
	tags: '/tags',
	rules: '/rules'
};

export type TimeMode = 'relative' | 'absolute';

export type Command =
	| { type: 'page'; page: Page }
	| { type: 'sort'; sort: Sort | 'toggle' }
	| { type: 'unviewed'; value: boolean | 'toggle' }
	/** id "" is all sources. */
	| { type: 'source'; id: string }
	| { type: 'tag'; id: string }
	/** id "" is every source. */
	| { type: 'refresh'; source: string }
	| { type: 'time'; mode: TimeMode | 'toggle' }
	| { type: 'follow' }
	| { type: 'help' };

export interface CommandContext {
	sources: readonly Source[];
	tags: readonly Tag[];
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
	{ name: 'sort', args: '[newest|oldest]', desc: 'set the sort order, or toggle it' },
	{ name: 'unviewed', args: '[on|off]', desc: 'show only unviewed items, or toggle it' },
	{ name: 'src', args: '<name>|all', desc: 'filter by source (:src alone lists sources)' },
	{ name: 'tag', args: '<name>|all', desc: 'filter by tag' },
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
			return arg ? fail(`${r.name} takes no argument`) : ok({ type: 'page', page: r.name });
		case 'help':
			return arg ? fail('help takes no argument') : ok({ type: 'help' });
		case 'follow':
			return arg ? fail('follow takes no argument') : ok({ type: 'follow' });
		case 'sort': {
			if (!arg) return ok({ type: 'sort', sort: 'toggle' });
			const s = oneOf(arg, ['newest', 'oldest'] as const);
			return s ? ok({ type: 'sort', sort: s }) : fail(`sort: newest or oldest, not ${arg}`);
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
			list = ['newest', 'oldest'];
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
