// A small Markdown subset for digest bodies, parsed to a typed tree. A digest
// is untrusted text (an LLM wrote it, and it can repeat the markup and the
// instructions of the feed it read), so this parser never produces HTML: the
// tree has no raw HTML node, no image node, and a link only where safeLink
// accepts its target. The renderer (Markdown.svelte) builds elements and text
// nodes from the tree and nothing else.
//
// Blocks: headings (# to ###; deeper levels count as ###), paragraphs, bullet
// and numbered lists (nested), block quotes, fenced code blocks, horizontal
// rules. Inlines: **strong**, *em*, `code`, [text](url), https:// autolinks
// and [#123] item references. Anything else, "<script>" included, is text.
//
// Work is bounded: the input is cut at MAX_INPUT, containers (lists, quotes)
// nest at most MAX_DEPTH deep and inline spans at most MAX_INLINE_DEPTH, and
// every search for a closing delimiter is cached so that a hostile body of
// unclosed markers costs time linear in its size.

import { safeLink } from './sanitize';

/** The most characters of a body that are parsed. The daemon allows 64 KiB. */
export const MAX_INPUT = 256 * 1024;
/** The deepest nesting of lists and quotes; deeper lines are plain paragraphs. */
export const MAX_DEPTH = 3;
/** The deepest nesting of inline spans (strong in em in a link); deeper markers are text. */
export const MAX_INLINE_DEPTH = 3;

export type Inline =
	| { t: 'text'; text: string }
	| { t: 'br' }
	| { t: 'strong'; children: Inline[] }
	| { t: 'em'; children: Inline[] }
	| { t: 'code'; text: string }
	/** href is what safeLink returned. */
	| { t: 'link'; href: string; children: Inline[] }
	/** [#123]: the renderer links it only when item 123 is one the digest is based on. */
	| { t: 'item'; id: string };

export type Block =
	| { t: 'heading'; level: 1 | 2 | 3; children: Inline[] }
	| { t: 'para'; children: Inline[] }
	| { t: 'list'; ordered: boolean; items: Block[][] }
	| { t: 'quote'; children: Block[] }
	| { t: 'code'; text: string }
	| { t: 'hr' };

// ── Blocks ────────────────────────────────────────────────────

const FENCE_RE = /^ {0,3}(`{3,}|~{3,})(.*)$/;
const HEADING_RE = /^ {0,3}(#{1,6})[ \t]+(.*)$/;
const HEADING_EMPTY_RE = /^ {0,3}#{1,6}[ \t]*$/;
const HR_RE = /^ {0,3}([-*_])(?:[ \t]*\1){2,}[ \t]*$/;
const QUOTE_RE = /^ {0,3}>/;
const LIST_RE = /^( {0,7})([-*+]|\d{1,9}[.)])( +)(\S.*)$/;

function expandTabs(line: string): string {
	let i = 0;
	while (i < line.length && (line[i] === ' ' || line[i] === '\t')) i++;
	if (i === 0 || !line.slice(0, i).includes('\t')) return line;
	return line.slice(0, i).replace(/\t/g, '    ') + line.slice(i);
}

/** The text of a heading without its closing "##" run (found by hand: a regex for it backtracks quadratically on long runs of spaces). */
function headingText(rest: string): string {
	const t = rest.trimEnd();
	let e = t.length;
	while (e > 0 && t[e - 1] === '#') e--;
	if (e < t.length && (e === 0 || t[e - 1] === ' ' || t[e - 1] === '\t')) return t.slice(0, e).trimEnd();
	return t;
}

function indentOf(line: string): number {
	let i = 0;
	while (i < line.length && line[i] === ' ') i++;
	return i;
}

const isBlank = (line: string) => line.trim() === '';

function isBlockStart(line: string, depth: number): boolean {
	if (FENCE_RE.test(line) || HEADING_RE.test(line) || HEADING_EMPTY_RE.test(line) || HR_RE.test(line)) return true;
	if (depth < MAX_DEPTH && (QUOTE_RE.test(line) || LIST_RE.test(line))) return true;
	return false;
}

/** Parses Markdown text to blocks. */
export function parseMarkdown(src: string): Block[] {
	const text = src.length > MAX_INPUT ? src.slice(0, MAX_INPUT) : src;
	return parseBlocks(text.split('\n').map(expandTabs), 0);
}

function parseBlocks(lines: string[], depth: number): Block[] {
	const out: Block[] = [];
	let i = 0;
	while (i < lines.length) {
		const line = lines[i] as string;
		if (isBlank(line)) {
			i++;
			continue;
		}

		const fence = FENCE_RE.exec(line);
		if (fence) {
			const mark = fence[1] as string;
			const close = new RegExp(`^ {0,3}${mark[0] === '`' ? '`' : '~'}{${mark.length},}[ \\t]*$`);
			const body: string[] = [];
			i++;
			while (i < lines.length && !close.test(lines[i] as string)) body.push(lines[i++] as string);
			i++; // the closing fence, or past the end
			out.push({ t: 'code', text: body.join('\n') });
			continue;
		}

		const heading = HEADING_RE.exec(line);
		if (heading || HEADING_EMPTY_RE.test(line)) {
			const level = Math.min((heading?.[1] ?? '#').length, 3) as 1 | 2 | 3;
			out.push({ t: 'heading', level, children: parseInlines(headingText(heading?.[2] ?? '')) });
			i++;
			continue;
		}

		if (HR_RE.test(line)) {
			out.push({ t: 'hr' });
			i++;
			continue;
		}

		if (depth < MAX_DEPTH && QUOTE_RE.test(line)) {
			const inner: string[] = [];
			while (i < lines.length) {
				const l = lines[i] as string;
				if (QUOTE_RE.test(l)) inner.push(l.replace(/^ {0,3}> ?/, ''));
				else if (!isBlank(l) && !isBlockStart(l, depth) && inner.length > 0 && !isBlank(inner[inner.length - 1] as string)) inner.push(l);
				else break;
				i++;
			}
			out.push({ t: 'quote', children: parseBlocks(inner, depth + 1) });
			continue;
		}

		if (depth < MAX_DEPTH && LIST_RE.test(line)) {
			i = parseList(lines, i, depth, out);
			continue;
		}

		// A paragraph runs to a blank line or the start of another block.
		const para: string[] = [line.trimStart()];
		i++;
		while (i < lines.length && !isBlank(lines[i] as string) && !isBlockStart(lines[i] as string, depth)) {
			para.push((lines[i++] as string).trimStart());
		}
		out.push({ t: 'para', children: parseInlines(para.join('\n')) });
	}
	return out;
}

/** Parses the list that starts at lines[start], pushes it to out, and returns the next line. */
function parseList(lines: string[], start: number, depth: number, out: Block[]): number {
	const first = LIST_RE.exec(lines[start] as string) as RegExpExecArray;
	const base = (first[1] as string).length;
	const ordered = /^\d/.test(first[2] as string);
	const items: Block[][] = [];
	let i = start;

	while (i < lines.length) {
		const m = LIST_RE.exec(lines[i] as string);
		if (!m || (m[1] as string).length > base + 1) break;
		const offset = (m[1] as string).length + (m[2] as string).length + (m[3] as string).length;
		const itemLines = [m[4] as string];
		i++;
		while (i < lines.length) {
			const l = lines[i] as string;
			if (isBlank(l)) break;
			const ind = indentOf(l);
			if (ind > base + 1 || LIST_RE.test(l) === false) {
				// Deeper lines belong to the item; so does a plain line that
				// continues its paragraph. A heading, rule or fence ends it.
				if (ind <= base + 1 && isBlockStart(l, depth)) break;
				itemLines.push(l.slice(Math.min(ind, offset)));
				i++;
			} else break;
		}
		items.push(parseBlocks(itemLines, depth + 1));
		// A blank line between items keeps the list going; anything else ends it.
		let j = i;
		while (j < lines.length && isBlank(lines[j] as string)) j++;
		const next = j < lines.length ? LIST_RE.exec(lines[j] as string) : null;
		if (next && (next[1] as string).length <= base + 1) i = j;
		else break;
	}
	out.push({ t: 'list', ordered, items });
	return i;
}

// ── Inlines ───────────────────────────────────────────────────

/**
 * Finds the next match of a pattern at or after a position, remembering the
 * answer: the parser only moves forward, so a search that found nothing (or
 * found something further on) is not repeated for every opening marker, and a
 * body of unclosed markers is parsed in linear time.
 */
class Finder {
	private cache = new Map<string, { from: number; pos: number; len: number }>();

	constructor(private text: string) {}

	find(key: string, re: RegExp, from: number): { pos: number; len: number } {
		const c = this.cache.get(key);
		if (c && from >= c.from && (c.pos === -1 || from <= c.pos)) return { pos: c.pos, len: c.len };
		re.lastIndex = from;
		const m = re.exec(this.text);
		const r = { from, pos: m ? m.index : -1, len: m ? m[0].length : 0 };
		this.cache.set(key, r);
		return { pos: r.pos, len: r.len };
	}
}

const STRONG_CLOSE = /\*\*/g;
const EM_CLOSE = /(?<!\*)\*(?!\*)/g;
/** The longest link target that is looked at. */
const MAX_URL = 2048;
/**
 * The most parentheses nested in a link target (CommonMark also stops at 32).
 * Every "](" holds a "(", so a target scan passes at most this many other
 * links: without it, a body of "[a](" repeated scanned MAX_URL characters for
 * each one.
 */
const MAX_URL_PARENS = 32;
const BRACKET_PAREN = /\]\(/g;
const ITEM_REF = /\[#(\d{1,18})\]/y;
const URL_RE = /https?:\/\/[^\s<>"]+/y;
const ESCAPABLE = '\\`*[]()#<>_-+.!|~{}';

/** Parses inline Markdown. */
export function parseInlines(text: string): Inline[] {
	return inlines(text, 0, false);
}

function inlines(text: string, depth: number, inLink: boolean): Inline[] {
	const out: Inline[] = [];
	const f = new Finder(text);
	const targets = new Map<number, { href: string; end: number } | null>();
	let buf = '';
	const flush = () => {
		if (buf) out.push({ t: 'text', text: buf });
		buf = '';
	};
	const nest = depth < MAX_INLINE_DEPTH;

	// The target of the link whose "](" is at pos: one URL without whitespace,
	// parentheses balanced, closed by ")". Remembered, so that the many "["
	// before one "](" do not each scan it again; the scan stops after
	// MAX_URL characters or MAX_URL_PARENS open parentheses, so all of them
	// together stay linear.
	// href is "" for a target safeLink rejects (the link then shows its
	// text), end is where the ")" is; null is no link.
	const target = (pos: number): { href: string; end: number } | null => {
		const known = targets.get(pos);
		if (known !== undefined) return known;
		let end = -1;
		let open = 0;
		for (let k = pos + 2; k < text.length && k - pos <= MAX_URL; k++) {
			const ch = text[k] as string;
			if (ch === '(') {
				if (++open > MAX_URL_PARENS) break;
			} else if (ch === ')') {
				if (open === 0) {
					end = k;
					break;
				}
				open--;
			} else if (/\s/.test(ch)) break;
		}
		const raw = end === -1 ? '' : text.slice(pos + 2, end);
		const r = end === -1 || raw === '' ? null : { href: safeLink(raw) ?? '', end };
		targets.set(pos, r);
		return r;
	};

	let i = 0;
	while (i < text.length) {
		const c = text[i] as string;

		if (c === '\n') {
			flush();
			out.push({ t: 'br' });
			i++;
			continue;
		}

		if (c === '\\' && i + 1 < text.length && ESCAPABLE.includes(text[i + 1] as string)) {
			buf += text[i + 1];
			i += 2;
			continue;
		}

		if (c === '`') {
			let n = 1;
			while (text[i + n] === '`') n++;
			const close = f.find('code' + n, new RegExp('(?<!`)`{' + n + '}(?!`)', 'g'), i + n);
			if (close.pos !== -1) {
				flush();
				out.push({ t: 'code', text: text.slice(i + n, close.pos).replace(/\n/g, ' ') });
				i = close.pos + n;
			} else {
				buf += text.slice(i, i + n);
				i += n;
			}
			continue;
		}

		if (c === '*' && nest) {
			if (text[i + 1] === '*' && i + 2 < text.length && !/\s/.test(text[i + 2] as string)) {
				const close = f.find('strong', STRONG_CLOSE, i + 2).pos;
				if (close > i + 2) {
					flush();
					out.push({ t: 'strong', children: inlines(text.slice(i + 2, close), depth + 1, inLink) });
					i = close + 2;
					continue;
				}
			} else if (text[i + 1] !== '*' && i + 1 < text.length && !/\s/.test(text[i + 1] as string)) {
				const close = f.find('em', EM_CLOSE, i + 1).pos;
				if (close > i + 1) {
					flush();
					out.push({ t: 'em', children: inlines(text.slice(i + 1, close), depth + 1, inLink) });
					i = close + 1;
					continue;
				}
			}
			// A run of unmatched stars is text: take the whole run, so
			// each of its stars is not tried again.
			let n = 1;
			while (text[i + n] === '*') n++;
			buf += text.slice(i, i + n);
			i += n;
			continue;
		}

		if (c === '[') {
			ITEM_REF.lastIndex = i;
			const ref = ITEM_REF.exec(text);
			if (ref) {
				flush();
				out.push({ t: 'item', id: ref[1] as string });
				i += ref[0].length;
				continue;
			}
			if (nest && !inLink) {
				const mid = f.find('bracket', BRACKET_PAREN, i + 1).pos;
				if (mid !== -1) {
					const tg = target(mid);
					if (tg !== null) {
						const children = inlines(text.slice(i + 1, mid), depth + 1, true);
						flush();
						// A target safeLink rejects leaves the link's text.
						if (tg.href) out.push({ t: 'link', href: tg.href, children });
						else out.push(...children);
						i = tg.end + 1;
						continue;
					}
				}
			}
			buf += c;
			i++;
			continue;
		}

		if (c === 'h' && !inLink && (i === 0 || !/[\w/]/.test(text[i - 1] as string))) {
			URL_RE.lastIndex = i;
			const m = URL_RE.exec(text);
			if (m) {
				// Trailing punctuation belongs to the sentence, not the URL.
				const url = m[0].replace(/[.,;:!?)\]'*]+$/, '');
				const href = safeLink(url);
				flush();
				if (href) out.push({ t: 'link', href, children: [{ t: 'text', text: url }] });
				else out.push({ t: 'text', text: url });
				i += url.length;
				if (url.length === 0) i++;
				continue;
			}
		}

		buf += c;
		i++;
	}
	flush();
	return out;
}
