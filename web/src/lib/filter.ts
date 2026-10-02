import type { Filter, Sort } from './types';

export const defaultFilter: Filter = {
	q: '',
	source: '',
	tag: '',
	sort: 'newest',
	unviewed: false,
	assessor: '',
	minScore: null,
	unassessed: ''
};

const ID_RE = /^[1-9][0-9]{0,18}$/;
const MAX_QUERY = 500;

/**
 * Reads the filter from the page URL (?q=&source=&tag=&sort=&unviewed=1
 * &assessor=&min_score=&unassessed=). Anything malformed falls back to the
 * default, so a hand-edited or stale bookmark never produces a request the
 * API rejects: a minimum score or the score sort without an assessor is
 * dropped, like the daemon would refuse it.
 */
export function filterFromParams(p: URLSearchParams): Filter {
	const source = p.get('source') ?? '';
	const tag = p.get('tag') ?? '';
	const sort = p.get('sort');
	const unviewed = p.get('unviewed');
	const assessorParam = p.get('assessor') ?? '';
	const assessor = ID_RE.test(assessorParam) ? assessorParam : '';
	const unassessed = p.get('unassessed') ?? '';
	return {
		q: (p.get('q') ?? '').slice(0, MAX_QUERY),
		source: ID_RE.test(source) ? source : '',
		tag: ID_RE.test(tag) ? tag : '',
		sort: sort === 'oldest' ? 'oldest' : sort === 'score' && assessor ? 'score' : 'newest',
		unviewed: unviewed === '1' || unviewed === 'true',
		assessor,
		minScore: assessor ? parseScore(p.get('min_score')) : null,
		unassessed: ID_RE.test(unassessed) ? unassessed : ''
	};
}

/** A score from 0 to 1 as text (a URL parameter), or null. */
export function parseScore(s: string | null | undefined): number | null {
	const t = s?.trim() ?? '';
	if (!/^(\d+\.?\d*|\.\d+)$/.test(t)) return null;
	const n = Number(t);
	return n >= 0 && n <= 1 ? n : null;
}

/**
 * Encodes a filter as query parameters, leaving out defaults. The same
 * parameters are used by the page URL, /api/stream and /api/items.
 */
export function filterToParams(f: Filter): URLSearchParams {
	const p = new URLSearchParams();
	if (f.q) p.set('q', f.q);
	if (f.source) p.set('source', f.source);
	if (f.tag) p.set('tag', f.tag);
	if (f.sort !== 'newest') p.set('sort', f.sort);
	if (f.unviewed) p.set('unviewed', '1');
	if (f.assessor) p.set('assessor', f.assessor);
	if (f.assessor && f.minScore !== null) p.set('min_score', String(f.minScore));
	if (f.unassessed) p.set('unassessed', f.unassessed);
	return p;
}

/** "?a=b" or "" for the default filter. */
export function filterQuery(f: Filter): string {
	const s = filterToParams(f).toString();
	return s ? '?' + s : '';
}

export function sameFilter(a: Filter, b: Filter): boolean {
	return filterQuery(a) === filterQuery(b);
}

/** The next value in a cycle of IDs where "" (all) comes first, like the TUI. */
export function cycleID(ids: string[], current: string): string {
	const all = ['', ...ids];
	const i = all.indexOf(current);
	return all[(i + 1) % all.length] ?? '';
}

/** The o key: newest and oldest first take turns; the score sort goes back to newest. */
export function nextSort(s: Sort): Sort {
	return s === 'newest' ? 'oldest' : 'newest';
}

/**
 * The filter with another assessor (id "" is none). Without an assessor a
 * minimum score and the score sort mean nothing, so they go with it; the
 * "not assessed by" filter stays, it names its own assessor.
 */
export function withAssessor(f: Filter, id: string): Filter {
	if (id !== '') return { ...f, assessor: id };
	return { ...f, assessor: '', minScore: null, sort: f.sort === 'score' ? 'newest' : f.sort };
}
