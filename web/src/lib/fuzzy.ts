// Fuzzy matching for the S/T pickers: the query's characters must appear in
// the label in order (case-insensitive). Scoring prefers a match at the
// start, at word starts, and in one run; ties keep the original order.

export interface FuzzyMatch<T> {
	item: T;
	/** Indexes into the label of the matched characters, for highlighting. */
	at: number[];
	score: number;
}

const isBoundary = (label: string, i: number) => i === 0 || /[\s\-_./:]/.test(label[i - 1]!);

/** Matches query against label, or null when it is not a subsequence. */
export function fuzzyScore(label: string, query: string): { at: number[]; score: number } | null {
	const l = label.toLowerCase();
	const q = query.toLowerCase().replace(/\s+/g, '');
	if (q === '') return { at: [], score: 0 };
	// Greedy, but each character prefers a word start if one is ahead in reach.
	const at: number[] = [];
	let score = 0;
	let from = 0;
	for (let k = 0; k < q.length; k++) {
		const ch = q[k]!;
		let i = l.indexOf(ch, from);
		if (i < 0) return null;
		// Look for a word-start occurrence before settling for this one.
		if (!isBoundary(label, i)) {
			let j = i;
			while ((j = l.indexOf(ch, j + 1)) >= 0) {
				if (isBoundary(label, j)) {
					i = j;
					break;
				}
			}
		}
		const prev = at[at.length - 1];
		if (i === 0) score += 8;
		else if (isBoundary(label, i)) score += 5;
		if (prev !== undefined && i === prev + 1) score += 6;
		// A gap costs; so does starting late, unless it is at a word start.
		if (prev !== undefined) score -= Math.min(i - prev - 1, 5);
		else if (!isBoundary(label, i)) score -= Math.min(i, 10);
		at.push(i);
		from = i + 1;
	}
	// Shorter labels win over longer ones with the same match.
	score -= Math.floor(l.length / 8);
	return { at, score };
}

/** The items whose label matches, best first; all of them, in order, for an empty query. */
export function fuzzyFilter<T>(items: readonly T[], query: string, label: (item: T) => string): FuzzyMatch<T>[] {
	const out: (FuzzyMatch<T> & { order: number })[] = [];
	items.forEach((item, order) => {
		const m = fuzzyScore(label(item), query);
		if (m) out.push({ item, ...m, order });
	});
	out.sort((a, b) => b.score - a.score || a.order - b.order);
	return out;
}
