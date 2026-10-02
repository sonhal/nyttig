import { describe, expect, it } from 'vitest';
import { defaultFilter } from './filter';
import { applyCompletion, complete, format, parse } from './query';
import type { Filter, Source, Tag } from './types';

const sources: Source[] = [
	{ id: '1', name: 'Alpha News', abbreviation: 'ALP' },
	{ id: '2', name: 'Hacker News', abbreviation: 'HN', color: '#FF6600' },
	{ id: '3', name: 'Go' }
];
const tags: Tag[] = [
	{ id: '1', name: 'linux' },
	{ id: '2', name: 'Rust' },
	{ id: '3', name: 'Release notes', color: '#00FF00' }
];

const ok = (text: string): Filter => {
	const r = parse(text, sources, tags);
	expect(r.errors).toEqual([]);
	return r.filter;
};
const f = (p: Partial<Filter>): Filter => ({ ...defaultFilter, ...p });

describe('parse', () => {
	it('reads free text and every operator', () => {
		expect(ok('')).toEqual(defaultFilter);
		expect(ok('kernel panic')).toEqual(f({ q: 'kernel panic' }));
		expect(ok('tag:linux')).toEqual(f({ tag: '1' }));
		expect(ok('src:Go')).toEqual(f({ source: '3' }));
		expect(ok('is:unviewed')).toEqual(f({ unviewed: true }));
		expect(ok('sort:oldest')).toEqual(f({ sort: 'oldest' }));
		expect(ok('kernel tag:linux src:go is:unviewed sort:oldest panic')).toEqual(
			f({ q: 'kernel panic', tag: '1', source: '3', unviewed: true, sort: 'oldest' })
		);
	});

	it('reads since: windows', () => {
		expect(ok('since:7d')).toEqual(f({ since: '7d' }));
		expect(ok('since:24h kernel')).toEqual(f({ since: '24h', q: 'kernel' }));
		expect(ok('SINCE:1mo')).toEqual(f({ since: '1mo' }));
		expect(ok('tag:linux since:2w is:unviewed sort:oldest')).toEqual(
			f({ tag: '1', since: '2w', unviewed: true, sort: 'oldest' })
		);
		expect(ok('since:7d since:7d')).toEqual(f({ since: '7d' }));
	});

	it('explains a bad since: window', () => {
		const msg = (text: string) => parse(text, sources, tags).errors[0]?.message;
		expect(msg('since:1m')).toContain('ambiguous');
		expect(msg('since:0d')).toContain('between 1 and 9999');
		expect(msg('since:7D')).toContain('not a window');
		expect(msg('since:')).toContain('needs a window');
		expect(msg('since:soon')).toContain('not a window');
		expect(msg('since:7d since:30d')).toBe('since given twice');
		expect(parse('since:1m', sources, tags).filter.since).toBe('');
	});

	it('ignores case in keys, values and names, and extra whitespace', () => {
		expect(ok('  TAG:RUST   Src:hn\tIS:Unviewed SORT:Oldest ')).toEqual(
			f({ tag: '2', source: '2', unviewed: true, sort: 'oldest' })
		);
		expect(ok('  a   b  ')).toEqual(f({ q: 'a b' }));
	});

	it('takes names with spaces in quotes', () => {
		expect(ok('src:"Hacker News"')).toEqual(f({ source: '2' }));
		expect(ok('tag:"release notes" x')).toEqual(f({ tag: '3', q: 'x' }));
		// Unterminated while typing: the quote runs to the end.
		expect(ok('tag:"Release notes')).toEqual(f({ tag: '3' }));
	});

	it('accepts source abbreviations and "source:" as an alias', () => {
		expect(ok('src:HN')).toEqual(f({ source: '2' }));
		expect(ok('source:alp')).toEqual(f({ source: '1' }));
	});

	it('prefers an exact name over an abbreviation or another case', () => {
		const tagsDup: Tag[] = [
			{ id: '1', name: 'go' },
			{ id: '2', name: 'Go' }
		];
		expect(parse('tag:Go', [], tagsDup).filter.tag).toBe('2');
		expect(parse('tag:GO', [], tagsDup).filter.tag).toBe('1');
	});

	it('reads #id as an ID, and quoted #id as a name', () => {
		expect(parse('src:#7 tag:#9', [], []).filter).toEqual(f({ source: '7', tag: '9' }));
		expect(parse('tag:#abc', [], tags).errors[0]?.message).toContain('bad tag id');
		expect(parse('tag:"#2"', [], [{ id: '5', name: '#2' }]).filter.tag).toBe('5');
	});

	it('quoted words and unknown keys are free text', () => {
		expect(ok('"tag:linux"')).toEqual(f({ q: 'tag:linux' }));
		expect(ok('"two words" more')).toEqual(f({ q: 'two words more' }));
		expect(ok('http://example.com foo:bar')).toEqual(f({ q: 'http://example.com foo:bar' }));
		expect(ok('say "hi \\"there\\""')).toEqual(f({ q: 'say hi "there"' }));
	});

	it('reports unknown names with the token range instead of dropping them', () => {
		const r = parse('kernel tag:nope src:"No Such" is:old sort:sideways', sources, tags);
		expect(r.errors.map((e) => e.message)).toEqual([
			'unknown tag: nope',
			'unknown source: No Such',
			'unknown is: value: old (try unviewed)',
			'unknown sort: sideways (newest or oldest)'
		]);
		const first = r.errors[0]!;
		expect('kernel tag:nope src:"No Such"'.slice(first.start, first.end)).toBe('tag:nope');
		// What is valid still applies.
		expect(r.filter.q).toBe('kernel');
		expect(r.filter.tag).toBe('');
	});

	it('reports empty values', () => {
		expect(parse('tag:', sources, tags).errors[0]?.message).toBe('tag: needs a tag name');
		expect(parse('src:', sources, tags).errors[0]?.message).toBe('src: needs a source name');
		expect(parse('is:', sources, tags).errors[0]?.message).toContain('needs a value');
		expect(parse('sort:', sources, tags).errors[0]?.message).toContain('needs newest or oldest');
	});

	it('repeated keys: the same value is fine, a different one is an error', () => {
		expect(ok('tag:linux tag:LINUX is:unviewed is:unviewed sort:oldest sort:oldest')).toEqual(
			f({ tag: '1', unviewed: true, sort: 'oldest' })
		);
		expect(parse('tag:linux tag:rust', sources, tags).errors[0]?.message).toBe('only one tag at a time');
		expect(parse('src:go src:hn', sources, tags).errors[0]?.message).toBe('only one source at a time');
		expect(parse('sort:oldest sort:newest', sources, tags).errors[0]?.message).toBe('sort given twice');
	});

	it('rejects search text over the API limit', () => {
		const r = parse('x'.repeat(501), sources, tags);
		expect(r.errors[0]?.message).toContain('too long');
		expect(r.filter.q).toHaveLength(500);
	});
});

describe('format', () => {
	it('puts free text first, then the operators in a fixed order', () => {
		expect(format(defaultFilter, sources, tags)).toBe('');
		expect(format(f({ q: 'kernel  panic', tag: '1', source: '3', unviewed: true, sort: 'oldest' }), sources, tags)).toBe(
			'kernel panic src:Go tag:linux is:unviewed sort:oldest'
		);
		expect(format(f({ q: 'kernel', tag: '1', unviewed: true, since: '7d', sort: 'oldest' }), sources, tags)).toBe(
			'kernel tag:linux is:unviewed since:7d sort:oldest'
		);
	});

	it('quotes names with spaces, and words that would be read as operators', () => {
		expect(format(f({ source: '2', tag: '3' }), sources, tags)).toBe('src:"Hacker News" tag:"Release notes"');
		expect(format(f({ q: 'tag:x "y z' }), sources, tags)).toBe('"tag:x" "\\"y" z');
		expect(format(f({ q: 'http://x.org 5" foo:bar' }), sources, tags)).toBe('http://x.org 5" foo:bar');
	});

	it('writes #id when the name is unknown or ambiguous', () => {
		expect(format(f({ source: '9', tag: '8' }), sources, tags)).toBe('src:#9 tag:#8');
		expect(format(f({ source: '9' }), [], [])).toBe('src:#9');
		const dup: Tag[] = [
			{ id: '1', name: 'Go' },
			{ id: '2', name: 'Go' }
		];
		expect(format(f({ tag: '1' }), [], dup)).toBe('tag:Go');
		expect(format(f({ tag: '2' }), [], dup)).toBe('tag:#2');
		expect(format(f({ tag: '4' }), [], [{ id: '4', name: '#x' }])).toBe('tag:"#x"');
	});
});

describe('round trips', () => {
	const filters: Filter[] = [
		defaultFilter,
		f({ q: 'kernel panic' }),
		f({ tag: '3', source: '2' }),
		f({ since: '30d' }),
		f({ q: 'since:7d', since: '1y', tag: '1' }),
		f({ q: 'tag:linux', tag: '1' }),
		f({ q: '"quoted" \\back "x', unviewed: true }),
		f({ q: 'sort:oldest is:unviewed src:x', sort: 'oldest' }),
		f({ source: '9', tag: '8' }),
		f({ q: 'ünïcode ☃ 日本語', source: '1' })
	];
	it.each(filters.map((x, i) => [i, x] as const))('parse(format(f)) gives f back (%i)', (_, filter) => {
		const text = format(filter, sources, tags);
		const r = parse(text, sources, tags);
		expect(r.errors).toEqual([]);
		expect(r.filter).toEqual(filter);
		// And the text is canonical.
		expect(format(r.filter, sources, tags)).toBe(text);
	});

	it.each([
		'  b   a  TAG:rust   is:UNVIEWED ',
		'src:"hacker news" x',
		'"a b" c sort:OLDEST',
		'src:#4 tag:#5'
	])('format(parse(text)) is stable for %j', (text) => {
		const once = format(parse(text, sources, tags).filter, sources, tags);
		expect(format(parse(once, sources, tags).filter, sources, tags)).toBe(once);
	});
});

describe('complete', () => {
	const at = (text: string, caret = text.length) => complete(text, caret, sources, tags);

	it('offers nothing outside an operator value', () => {
		expect(at('')).toBeNull();
		expect(at('kern')).toBeNull();
		expect(at('tag:rust ')).toBeNull();
		expect(at('"tag:ru')).toBeNull();
	});

	it('completes tag and source names, prefix matches first', () => {
		expect(at('tag:')?.candidates.map((c) => c.label)).toEqual(['linux', 'Rust', 'Release notes']);
		expect(at('tag:r')?.candidates.map((c) => c.label)).toEqual(['Rust', 'Release notes']);
		expect(at('tag:zz')?.candidates.map((c) => c.label)).toEqual([]);
		expect(at('tag:ust')?.candidates.map((c) => c.label)).toEqual(['Rust']);
		expect(at('src:hack')?.candidates.map((c) => c.insert)).toEqual(['src:"Hacker News"']);
		expect(at('src:news')?.candidates.map((c) => c.label)).toEqual(['Alpha News', 'Hacker News']);
		expect(at('is:')?.candidates.map((c) => c.insert)).toEqual(['is:unviewed']);
		expect(at('sort:o')?.candidates.map((c) => c.insert)).toEqual(['sort:oldest']);
	});

	it('suggests common since: windows', () => {
		expect(at('since:')?.candidates.map((c) => c.insert)).toEqual([
			'since:24h',
			'since:7d',
			'since:2w',
			'since:1mo',
			'since:1y'
		]);
		expect(at('since:1')?.candidates.map((c) => c.insert)).toEqual(['since:1mo', 'since:1y']);
		expect(at('since:7d')).toBeNull();
		expect(at('since:2')?.candidates[0]?.kind).toBe('since');
	});

	it('carries the color for the list', () => {
		expect(at('src:hack')?.candidates[0]?.color).toBe('#FF6600');
	});

	it('completes inside a quote and mid-text', () => {
		expect(at('src:"hack')?.candidates.map((c) => c.label)).toEqual(['Hacker News']);
		const text = 'a tag:ru b';
		const c = complete(text, 'a tag:ru'.length, sources, tags)!;
		expect(c.candidates[0]?.label).toBe('Rust');
		expect(applyCompletion(text, c, c.candidates[0]!)).toEqual({ text: 'a tag:Rust b', caret: 'a tag:Rust '.length });
	});

	it('adds a space after the name at the end of the text', () => {
		const c = at('x src:hack')!;
		expect(applyCompletion('x src:hack', c, c.candidates[0]!)).toEqual({
			text: 'x src:"Hacker News" ',
			caret: 'x src:"Hacker News" '.length
		});
	});

	it('has nothing to add once the name is complete', () => {
		expect(at('tag:Rust')).toBeNull();
		expect(at('tag:rust')?.candidates.map((c) => c.label)).toEqual(['Rust']);
	});

	it('limits the list', () => {
		const many: Tag[] = Array.from({ length: 30 }, (_, i) => ({ id: String(i + 1), name: 'tag' + i }));
		expect(complete('tag:', 4, [], many)?.candidates).toHaveLength(8);
	});
});
