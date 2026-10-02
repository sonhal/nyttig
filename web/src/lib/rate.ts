// Rating items yourself: ":rate <score> [note]" writes an assessment as the
// built-in assessor "me", which is created the first time it is needed. Your
// scores are a ground truth to compare the other assessors against. The
// score is for the item as a whole.

import * as api from './api';
import { parseScore } from './filter';
import type { Assessor } from './types';

/** The built-in assessor's name. */
export const ME = 'me';
export const ME_DESCRIPTION = 'Your own ratings, 0 to 1';
export const ME_COLOR = '#4EC9B0';

export type RateParse = { ok: true; score: number; note: string } | { ok: false; error: string };

const USAGE = 'rate: a score from 0 to 1, then an optional note';

/** Reads the argument of ":rate": a score, then the rest of the line as the note. */
export function parseRate(arg: string): RateParse {
	const text = arg.trim();
	if (text === '') return { ok: false, error: USAGE };
	const space = text.search(/\s/);
	const word = space < 0 ? text : text.slice(0, space);
	const score = parseScore(word);
	if (score === null) return { ok: false, error: `${USAGE}, not ${word}` };
	return { ok: true, score, note: space < 0 ? '' : text.slice(space).trim() };
}

/** The ID of the assessor "me", creating it when there is none (or when another client just did). */
export async function ensureMe(assessors: readonly Assessor[]): Promise<string> {
	const have = assessors.find((a) => a.name === ME)?.id;
	if (have) return have;
	try {
		const created = await api.addAssessor({ name: ME, description: ME_DESCRIPTION, color: ME_COLOR });
		if (created.id) return created.id;
	} catch (e) {
		// Another client created it first: use that one.
		if (!(e instanceof api.ApiError) || e.status !== 409) throw e;
	}
	const id = (await api.listAssessors()).find((a) => a.name === ME)?.id;
	if (!id) throw new Error('could not find or create the assessor "me"');
	return id;
}
