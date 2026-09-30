// Search highlighting. The text is split into segments and rendered as text
// nodes and <mark> elements, never as HTML, so feed text stays inert.
//
// The daemon matches with FTS5 (unicode61): text is split into tokens of
// letters and digits, compared ignoring case and diacritics, and the search
// text is matched as a phrase. That cannot be reproduced exactly here, so
// this approximates it word by word: every word of the search text marks the
// words of the item that are equal to it, ignoring case and diacritics. There
// is no prefix or substring matching (FTS5 has none either, so "rust" does not
// mark "trustworthy"), and no check that the words are adjacent.

export interface Segment {
	text: string;
	match: boolean;
}

/** Characters FTS5's unicode61 tokenizer keeps in a token: letters, numbers and private use. */
const WORD = /[\p{L}\p{N}\p{Co}]+/gu;
const MARKS = /\p{M}/gu;

/** Case- and diacritic-insensitive form of a word. */
function fold(s: string): string {
	return s.normalize('NFD').replace(MARKS, '').toLowerCase();
}

const MAX_TERMS = 16;

/** The distinct words of the free-text query, folded. */
export function highlightTerms(q: string): string[] {
	const terms = new Set<string>();
	for (const m of q.matchAll(WORD)) {
		const t = fold(m[0]);
		if (t) terms.add(t);
		if (terms.size >= MAX_TERMS) break;
	}
	return [...terms];
}

/** Splits text into matching and non-matching segments; the pieces join back to text. */
export function segments(text: string, terms: readonly string[]): Segment[] {
	if (!text) return [];
	if (terms.length === 0) return [{ text, match: false }];
	const out: Segment[] = [];
	let last = 0;
	for (const m of text.matchAll(WORD)) {
		if (!terms.includes(fold(m[0]))) continue;
		const at = m.index;
		if (at > last) out.push({ text: text.slice(last, at), match: false });
		out.push({ text: m[0], match: true });
		last = at + m[0].length;
	}
	if (last < text.length) out.push({ text: text.slice(last), match: false });
	return out.length ? out : [{ text, match: false }];
}

/** Segments for a range list (fuzzy picker): matched character indexes are marked. */
export function segmentsAt(text: string, marked: readonly number[]): Segment[] {
	if (marked.length === 0) return text ? [{ text, match: false }] : [];
	const set = new Set(marked);
	const out: Segment[] = [];
	let cur = '';
	let curMatch = false;
	for (let i = 0; i < text.length; i++) {
		const m = set.has(i);
		if (i > 0 && m !== curMatch) {
			out.push({ text: cur, match: curMatch });
			cur = '';
		}
		cur += text[i];
		curMatch = m;
	}
	if (cur) out.push({ text: cur, match: curMatch });
	return out;
}
