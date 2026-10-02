import { describe, expect, it } from 'vitest';
import { formatScore, inScope, scoreChips, scoreOf, tagScope } from './scores';
import type { Assessment, Item, Tag } from './types';

const a = (assessor: string, score: number | undefined, tag?: string, name = `as${assessor}`): Assessment => ({
	id: `${assessor}-${tag ?? 0}`,
	assessor_id: assessor,
	assessor_name: name,
	tag_id: tag,
	score
});
const item = (...assessments: Assessment[]): Item => ({ id: '1', assessments });

const tags: Tag[] = [
	{ id: '1', name: 'CVE' },
	{ id: '2', name: 'critical', parent_ids: ['1'] },
	{ id: '3', name: 'linux' }
];

describe('formatScore', () => {
	it('shows at most two decimals without trailing zeros', () => {
		expect(formatScore(0.9)).toBe('0.9');
		expect(formatScore(0.95)).toBe('0.95');
		expect(formatScore(0.123456)).toBe('0.12');
		expect(formatScore(1)).toBe('1');
		expect(formatScore(0)).toBe('0');
		expect(formatScore(0.3 + 0.6)).toBe('0.9');
	});
});

describe('scope', () => {
	it('is the tag and the tags below it, or everything without a tag filter', () => {
		expect([...tagScope(tags, '1')!].sort()).toEqual(['1', '2']);
		expect([...tagScope(tags, '2')!]).toEqual(['2']);
		expect(tagScope(tags, '')).toBeNull();
		const s = tagScope(tags, '1');
		expect(inScope(a('1', 0.5), s)).toBe(true); // a whole-item assessment
		expect(inScope(a('1', 0.5, '2'), s)).toBe(true);
		expect(inScope(a('1', 0.5, '3'), s)).toBe(false);
		expect(inScope(a('1', 0.5, '3'), null)).toBe(true);
	});
});

describe('scoreOf', () => {
	const it1 = item(a('1', 0.5), a('1', 0.9, '2'), a('1', 0.95, '3'), a('2', 1), a('1', undefined, '1'));

	it('is the highest in-scope score of the assessor', () => {
		expect(scoreOf(it1, { assessor: '1', tags: null })).toBe(0.95);
		expect(scoreOf(it1, { assessor: '1', tags: tagScope(tags, '1') })).toBe(0.9);
		expect(scoreOf(it1, { assessor: '1', tags: tagScope(tags, '2') })).toBe(0.9);
		expect(scoreOf(it1, { assessor: '1', tags: tagScope(tags, '3') })).toBe(0.95);
		expect(scoreOf(it1, { assessor: '2', tags: null })).toBe(1);
	});

	it('is null for no score: other assessors, notes, or an empty item', () => {
		expect(scoreOf(it1, { assessor: '9', tags: null })).toBeNull();
		expect(scoreOf(item(a('1', undefined)), { assessor: '1', tags: null })).toBeNull();
		expect(scoreOf({ id: '2' }, { assessor: '1', tags: null })).toBeNull();
		// 0 is a score.
		expect(scoreOf(item(a('1', 0)), { assessor: '1', tags: null })).toBe(0);
	});
});

describe('scoreChips', () => {
	it('one chip per assessor with its highest score, the selected first, the rest by name', () => {
		const it1 = item(a('2', 0.3, undefined, 'zed'), a('1', 0.4, undefined, 'claude'), a('1', 0.8, '3', 'claude'), a('3', 0.1, undefined, 'abc'));
		expect(scoreChips(it1, '').map((c) => [c.name, c.score])).toEqual([
			['abc', 0.1],
			['claude', 0.8],
			['zed', 0.3]
		]);
		expect(scoreChips(it1, '2').map((c) => c.name)).toEqual(['zed', 'abc', 'claude']);
	});

	it('leaves out notes without a score, and keeps a zero', () => {
		expect(scoreChips(item(a('1', undefined)), '')).toEqual([]);
		expect(scoreChips(item(a('1', 0)), '')).toHaveLength(1);
		expect(scoreChips({ id: '1' }, '')).toEqual([]);
	});
});
