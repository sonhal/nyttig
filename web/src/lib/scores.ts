// Assessment scores in the browser: which chips a row shows, and the score
// the "score" sort orders by. Pure functions, so they are unit-tested.
//
// Scores of different assessors are never combined: a chip is one
// assessor's, and the sort names one assessor. An assessment is in scope for
// a tag filter like the daemon decides it (see the filter semantics in
// docs/assessments-plan.md): when there is no tag filter, when it is for the
// item as a whole (no tag), or when its tag is the filter tag or below it.

import type { Assessment, Item } from './types';
import { descendants } from './tagtree';
import type { Tag } from './types';

/** Whose scores order the list, and which assessments count (null scope = all). */
export interface ScoreScope {
	assessor: string;
	/** The filter tag and the tags below it; null when there is no tag filter. */
	tags: ReadonlySet<string> | null;
}

/** The tag IDs an assessment must be for to count under a tag filter ("" = no filter, null). */
export function tagScope(tags: readonly Tag[], tagId: string): Set<string> | null {
	return tagId === '' ? null : new Set([tagId, ...descendants(tags, tagId)]);
}

/** True when the assessment counts for the scope: no tag filter, a whole-item one, or a tag in the subtree. */
export function inScope(a: Assessment, tags: ReadonlySet<string> | null): boolean {
	return tags === null || !a.tag_id || tags.has(a.tag_id);
}

/** The highest in-scope score the assessor gave the item, or null (a note alone is no score). */
export function scoreOf(item: Item, s: ScoreScope): number | null {
	let best: number | null = null;
	for (const a of item.assessments ?? []) {
		if (a.assessor_id !== s.assessor || typeof a.score !== 'number' || !inScope(a, s.tags)) continue;
		if (best === null || a.score > best) best = a.score;
	}
	return best;
}

/** A score for display: at most two decimals, no trailing zeros (0.9, 0.95, 1, 0). */
export function formatScore(n: number): string {
	return String(Number(n.toFixed(2)));
}

export interface Chip {
	assessorId: string;
	/** The name the assessment carries (a current name from the assessor list wins when drawing). */
	name: string;
	score: number;
}

/**
 * One chip per assessor that scored the item: its highest score, whatever
 * the tag, the selected assessor first and the others by name.
 */
export function scoreChips(item: Item, selected: string): Chip[] {
	const best = new Map<string, Chip>();
	for (const a of item.assessments ?? []) {
		if (typeof a.score !== 'number' || !a.assessor_id) continue;
		const cur = best.get(a.assessor_id);
		if (!cur || a.score > cur.score) best.set(a.assessor_id, { assessorId: a.assessor_id, name: a.assessor_name ?? '', score: a.score });
	}
	return [...best.values()].sort((x, y) => {
		if ((x.assessorId === selected) !== (y.assessorId === selected)) return x.assessorId === selected ? -1 : 1;
		return x.name.localeCompare(y.name) || x.assessorId.localeCompare(y.assessorId);
	});
}
