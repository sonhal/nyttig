import { describe, expect, it } from 'vitest';
import {
	columnCandidates,
	formatColumns,
	knownColumns,
	normalizeColumns,
	parseColumns,
	shows,
	spellColumns,
	toggleColumn
} from './columns';

// The same cases are in internal/columns/columns_test.go; keep them in step.
const CASES: { in: string; want?: string; err?: string }[] = [
	{ in: '', want: '' },
	{ in: '  ', want: '' },
	{ in: 'title,source', want: 'source,title' },
	{ in: 'Title, SRC', want: 'source,title' },
	{ in: 'time,title', want: 'date,title' },
	{ in: 'description,scores,tag', want: 'tags,score,desc' },
	{ in: 'domain,desc,title,score,tags,source,date', want: '' },
	{ in: 'title,title', err: 'twice' },
	{ in: 'title,time,date', err: 'twice' },
	{ in: 'title,bogus', err: 'bogus' },
	{ in: ',', err: 'empty' },
	{ in: 'title,', err: 'empty' },
	// Excluding: every column but these.
	{ in: '-desc,-domain', want: 'date,source,tags,score,title' },
	{ in: '-DESCRIPTION, -src', want: 'date,tags,score,title,domain' },
	{ in: '- desc', want: 'date,source,tags,score,title,domain' },
	{ in: '-title,source', err: 'mix' },
	{ in: '-date,-source,-tags,-score,-title,-desc,-domain', err: 'every column' },
	{ in: '-bogus', err: 'bogus' },
	{ in: '-title,-title', err: 'twice' },
	{ in: '-', err: 'empty' }
];

describe('parseColumns', () => {
	for (const c of CASES) {
		it(JSON.stringify(c.in), () => {
			const r = parseColumns(c.in);
			if (c.err) {
				expect(r.ok).toBe(false);
				if (!r.ok) expect(r.error).toContain(c.err);
			} else {
				expect(r).toEqual({ ok: true, columns: c.want ? c.want.split(',') : [] });
			}
		});
	}
});

describe('normalizeColumns', () => {
	it('takes canonical names only', () => {
		expect(normalizeColumns(['src']).ok).toBe(false);
		expect(normalizeColumns(['Title']).ok).toBe(false);
		expect(normalizeColumns(['title', 'date'])).toEqual({ ok: true, columns: ['date', 'title'] });
	});
});

describe('knownColumns', () => {
	it('drops what it does not know instead of failing', () => {
		expect(knownColumns(['title', 'author', 'source'])).toEqual(['source', 'title']);
		expect(knownColumns(['title', 'title'])).toEqual(['title']);
		expect(knownColumns(undefined)).toEqual([]);
		// Nothing known left: every column, never an empty row.
		expect(knownColumns(['author'])).toEqual([]);
	});
});

describe('shows', () => {
	it('shows everything for an empty selection', () => {
		expect(shows([], 'domain')).toBe(true);
		expect(shows(['title'], 'domain')).toBe(false);
		expect(shows(['title'], 'title')).toBe(true);
	});
});

describe('toggleColumn', () => {
	it('switches one column, in display order', () => {
		expect(toggleColumn(['title'], 'date')).toEqual(['date', 'title']);
		expect(toggleColumn(['date', 'title'], 'date')).toEqual(['title']);
	});
	it('starts from every column', () => {
		expect(toggleColumn([], 'desc')).toEqual(['date', 'source', 'tags', 'score', 'title', 'domain']);
	});
	it('gives every column back as []', () => {
		expect(toggleColumn(['date', 'source', 'tags', 'score', 'title', 'domain'], 'desc')).toEqual([]);
	});
	it('refuses to hide the last column', () => {
		expect(toggleColumn(['title'], 'title')).toBeNull();
	});
	it('formats with commas', () => {
		expect(formatColumns(['source', 'title'])).toBe('source,title');
	});
});

// The same cases are in internal/columns/columns_test.go (TestSpell).
describe('spellColumns', () => {
	for (const [input, want] of [
		['', ''],
		['title', 'title'],
		['source,title,score', 'source,score,title'],
		['date,source,tags,title', '-score,-desc,-domain'],
		['-desc,-domain', '-desc,-domain'],
		['-title', '-title']
	] as const) {
		it(input || '(all)', () => {
			const r = parseColumns(input);
			if (!r.ok) throw new Error(r.error);
			const got = spellColumns(r.columns);
			expect(got).toBe(want);
			expect(parseColumns(got)).toEqual(r);
		});
	}
});

describe('columnCandidates', () => {
	it('completes the part after the last comma', () => {
		expect(columnCandidates('ti')).toEqual({ offset: 0, candidates: ['title'] });
		expect(columnCandidates('title,d')).toEqual({ offset: 6, candidates: ['date', 'desc', 'domain'] });
		expect(columnCandidates('title, so')).toEqual({ offset: 7, candidates: ['source'] });
	});
	it('keeps the minus of a hidden column', () => {
		expect(columnCandidates('-desc,-do')).toEqual({ offset: 6, candidates: ['-domain'] });
		expect(columnCandidates('-de').candidates).toEqual(['-desc']);
	});
});
