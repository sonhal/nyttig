import { describe, expect, it } from 'vitest';
import {
	adjacentDigest,
	adjacentSeries,
	assessorDigestUsage,
	assessorDigestWarning,
	currentSeries,
	deleteSeriesLines,
	digestsHref,
	findSeries,
	formatLatest,
	formatPeriod,
	groupSeries,
	inputId,
	mergeDigests,
	moveWithinAssessor,
	selectionFromParams,
	seriesInListOrder,
	seriesLabel,
	seriesNames
} from './digests';
import type { Digest, DigestSeries } from './types';

// Display order: claude/daily, gpt/daily, claude/monthly, gpt/weekly, claude/yearly.
const series: DigestSeries[] = [
	{ id: '1', assessor_id: '10', assessor_name: 'claude', name: 'daily-cve', digest_count: 3, latest_period_end: '2026-10-05T23:59:59Z' },
	{ id: '2', assessor_id: '20', assessor_name: 'gpt', name: 'daily-cve', digest_count: 0 },
	{ id: '3', assessor_id: '10', assessor_name: 'claude', name: 'monthly', digest_count: 1 },
	{ id: '4', assessor_id: '20', assessor_name: 'gpt', name: 'weekly' },
	{ id: '5', assessor_id: '10', assessor_name: 'claude', name: 'yearly' }
];

describe('the URL', () => {
	it('reads the series and the digest', () => {
		expect(selectionFromParams(new URLSearchParams('series=3&digest=9'))).toEqual({ series: '3', digest: '9' });
		expect(selectionFromParams(new URLSearchParams('series=3'))).toEqual({ series: '3', digest: '' });
		expect(selectionFromParams(new URLSearchParams(''))).toEqual({ series: '', digest: '' });
	});

	it('ignores what is not an ID', () => {
		for (const bad of ['0', '-1', 'x', '1.5', '1e3', '01', ' 1', '99999999999999999999']) {
			expect(selectionFromParams(new URLSearchParams({ series: bad, digest: bad })), bad).toEqual({ series: '', digest: '' });
		}
	});

	it('builds the href', () => {
		expect(digestsHref()).toBe('/digests');
		expect(digestsHref('3')).toBe('/digests?series=3');
		expect(digestsHref('3', '9')).toBe('/digests?series=3&digest=9');
		expect(digestsHref('', '9')).toBe('/digests?digest=9');
	});

	it('round-trips', () => {
		const q = digestsHref('3', '9').split('?')[1] ?? '';
		expect(selectionFromParams(new URLSearchParams(q))).toEqual({ series: '3', digest: '9' });
	});
});

describe('series', () => {
	it('groups by assessor, in the order of the first series', () => {
		const groups = groupSeries(series);
		expect(groups.map((g) => [g.assessorName, g.series.map((s) => s.name)])).toEqual([
			['claude', ['daily-cve', 'monthly', 'yearly']],
			['gpt', ['daily-cve', 'weekly']]
		]);
		expect(seriesInListOrder(series).map((s) => s.id)).toEqual(['1', '3', '5', '2', '4']);
		expect(groupSeries([])).toEqual([]);
	});

	it('labels and names are one line', () => {
		expect(seriesLabel({ assessor_name: 'cla\nude', name: 'a  b\x1b' })).toBe('cla ude/a b');
		expect(seriesNames(series)[0]).toBe('claude/daily-cve');
	});

	it('walks the list the way the page shows it', () => {
		expect(adjacentSeries(series, '1', 1)?.id).toBe('3');
		expect(adjacentSeries(series, '5', 1)?.id).toBe('2');
		expect(adjacentSeries(series, '4', 1)).toBeUndefined();
		expect(adjacentSeries(series, '3', -1)?.id).toBe('1');
		expect(adjacentSeries(series, '1', -1)).toBeUndefined();
		// An unknown current series: the first (forward) or the last (backward).
		expect(adjacentSeries(series, '', 1)?.id).toBe('1');
		expect(adjacentSeries(series, '99', -1)?.id).toBe('4');
		expect(adjacentSeries([], '1', 1)).toBeUndefined();
	});

	it('shows the series asked for, else the first', () => {
		expect(currentSeries(series, '4')?.id).toBe('4');
		expect(currentSeries(series, '')?.id).toBe('1');
		expect(currentSeries(series, '99')?.id).toBe('1');
		expect(currentSeries([], '1')).toBeUndefined();
	});

	it('formats the latest period end as a date', () => {
		expect(formatLatest(series[0]!)).toBe('2026-10-05');
		expect(formatLatest(series[1]!)).toBe('');
		expect(formatLatest({ latest_period_end: 'nonsense' })).toBe('');
	});
});

describe('moveWithinAssessor', () => {
	it('swaps with the neighbor of the same assessor, keeping the others in place', () => {
		// Moving claude/monthly (3) up swaps it with claude/daily-cve (1), two places away in the full order.
		expect(moveWithinAssessor(series, '3', -1)).toEqual(['3', '2', '1', '4', '5']);
		expect(moveWithinAssessor(series, '3', 1)).toEqual(['1', '2', '5', '4', '3']);
		expect(moveWithinAssessor(series, '2', 1)).toEqual(['1', '4', '3', '2', '5']);
	});

	it('does not cross into another assessor, or past the ends', () => {
		expect(moveWithinAssessor(series, '1', -1)).toBeNull();
		expect(moveWithinAssessor(series, '5', 1)).toBeNull();
		expect(moveWithinAssessor(series, '4', 1)).toBeNull();
		expect(moveWithinAssessor(series, '99', 1)).toBeNull();
	});

	it('always lists every series exactly once', () => {
		for (const s of series) {
			for (const dir of [1, -1] as const) {
				const ids = moveWithinAssessor(series, s.id ?? '', dir);
				if (ids) expect([...ids].sort()).toEqual(['1', '2', '3', '4', '5']);
			}
		}
	});
});

describe('findSeries (for ":digest")', () => {
	it('finds assessor/name in any case', () => {
		expect(findSeries(series, 'claude/daily-cve')).toEqual({ ok: true, id: '1' });
		expect(findSeries(series, 'GPT/Daily-CVE')).toEqual({ ok: true, id: '2' });
		expect(findSeries(series, '  claude/monthly ')).toEqual({ ok: true, id: '3' });
	});

	it('accepts a name only when it is unique', () => {
		expect(findSeries(series, 'monthly')).toEqual({ ok: true, id: '3' });
		const r = findSeries(series, 'daily-cve');
		expect(r.ok).toBe(false);
		expect(!r.ok && r.error).toMatch(/ambiguous.*claude\/daily-cve.*gpt\/daily-cve/);
	});

	it('says what is wrong', () => {
		expect(findSeries(series, 'claude/nope')).toEqual({ ok: false, error: 'unknown series: claude/nope' });
		expect(findSeries(series, '')).toMatchObject({ ok: false });
		expect(findSeries([], 'claude/daily-cve')).toMatchObject({ ok: false });
	});
});

describe('history', () => {
	const d = (id: string): Digest => ({ id, title: 't' + id });

	it('merges a page of older digests without repeating one', () => {
		expect(mergeDigests([d('5'), d('4')], [d('4'), d('3'), d('2')]).map((x) => x.id)).toEqual(['5', '4', '3', '2']);
		expect(mergeDigests([], [d('1')])).toEqual([d('1')]);
		expect(mergeDigests([d('1')], [])).toEqual([d('1')]);
	});

	it('steps older and newer through a newest-first list', () => {
		const list = [d('5'), d('4'), d('3')];
		expect(adjacentDigest(list, '5', 1)?.id).toBe('4');
		expect(adjacentDigest(list, '4', -1)?.id).toBe('5');
		expect(adjacentDigest(list, '3', 1)).toBeUndefined();
		expect(adjacentDigest(list, '5', -1)).toBeUndefined();
		expect(adjacentDigest(list, '99', 1)).toBeUndefined();
	});

	it('finds the n-th input (1-based)', () => {
		const dg: Digest = { inputs: [{ id: '7' }, { id: '8' }] };
		expect(inputId(dg, 1)).toBe('7');
		expect(inputId(dg, 2)).toBe('8');
		expect(inputId(dg, 3)).toBeUndefined();
		expect(inputId(dg, 0)).toBeUndefined();
		expect(inputId({}, 1)).toBeUndefined();
		expect(inputId(undefined, 1)).toBeUndefined();
	});
});

describe('formatPeriod', () => {
	it('shows whole days as dates, in UTC', () => {
		expect(formatPeriod('2026-10-05T00:00:00Z', '2026-10-05T23:59:59Z')).toBe('2026-10-05');
		expect(formatPeriod('2026-10-05T00:00:00Z', '2026-10-05T00:00:00Z')).toBe('2026-10-05');
		expect(formatPeriod('2026-09-01T00:00:00Z', '2026-09-30T23:59:59Z')).toBe('2026-09-01 .. 2026-09-30');
		// 02:00 in +02:00 is midnight UTC: the page speaks UTC.
		expect(formatPeriod('2026-10-05T02:00:00+02:00', '2026-10-06T01:59:59+02:00')).toBe('2026-10-05');
	});

	it('shows the times of anything else', () => {
		expect(formatPeriod('2026-10-05T08:00:00Z', '2026-10-05T12:30:00Z')).toBe('2026-10-05 08:00Z .. 2026-10-05 12:30Z');
		expect(formatPeriod('2026-10-05T00:00:00Z', '2026-10-05T12:00:00Z')).toBe('2026-10-05 00:00Z .. 2026-10-05 12:00Z');
	});

	it('is empty for a missing or unreadable time', () => {
		expect(formatPeriod(undefined, '2026-10-05T00:00:00Z')).toBe('');
		expect(formatPeriod('2026-10-05T00:00:00Z', undefined)).toBe('');
		expect(formatPeriod('x', 'y')).toBe('');
	});
});

describe('what deleting takes with it', () => {
	it('says how many digests go with a series', () => {
		expect(deleteSeriesLines({ digest_count: 3 })[0]).toBe('3 digests will be deleted.');
		expect(deleteSeriesLines({ digest_count: 1 })[0]).toBe('1 digest will be deleted.');
		expect(deleteSeriesLines({})[0]).toBe('The series has no digests.');
	});

	it('counts an assessor\'s series and digests', () => {
		expect(assessorDigestUsage(series, '10')).toEqual({ series: 3, digests: 4 });
		expect(assessorDigestUsage(series, '20')).toEqual({ series: 2, digests: 0 });
		expect(assessorDigestUsage(series, '99')).toEqual({ series: 0, digests: 0 });
	});

	it('words the assessor warning, and says nothing when there is nothing', () => {
		expect(assessorDigestWarning({ series: 0, digests: 0 })).toBe('');
		expect(assessorDigestWarning({ series: 1, digests: 1 })).toBe('It has 1 digest series; 1 digest will be deleted with it.');
		expect(assessorDigestWarning({ series: 3, digests: 4 })).toBe('It has 3 digest series; 4 digests will be deleted with it.');
	});
});
