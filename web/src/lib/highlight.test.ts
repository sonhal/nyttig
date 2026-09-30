import { describe, expect, it } from 'vitest';
import { highlightTerms, segments, segmentsAt } from './highlight';

const join = (s: { text: string }[]) => s.map((x) => x.text).join('');
const marked = (s: { text: string; match: boolean }[]) => s.filter((x) => x.match).map((x) => x.text);

describe('highlightTerms', () => {
	it('takes the words, folded and distinct', () => {
		expect(highlightTerms('')).toEqual([]);
		expect(highlightTerms('Rust rust  RUST')).toEqual(['rust']);
		expect(highlightTerms('kernel-panic, "x"')).toEqual(['kernel', 'panic', 'x']);
		expect(highlightTerms('Café ÅNGSTRÖM')).toEqual(['cafe', 'angstrom']);
		expect(highlightTerms('!!! ???')).toEqual([]);
	});

	it('caps the number of terms', () => {
		expect(highlightTerms(Array.from({ length: 40 }, (_, i) => 'w' + i).join(' '))).toHaveLength(16);
	});
});

describe('segments', () => {
	it('splits into match and non-match pieces that join back to the text', () => {
		const text = 'Rust for Linux: the rust story';
		const s = segments(text, ['rust']);
		expect(join(s)).toBe(text);
		expect(marked(s)).toEqual(['Rust', 'rust']);
		expect(s[0]).toEqual({ text: 'Rust', match: true });
		expect(s[1]).toEqual({ text: ' for Linux: the ', match: false });
	});

	it('matches whole words only, like FTS5 tokens', () => {
		expect(marked(segments('trustworthy rusty rust', ['rust']))).toEqual(['rust']);
		expect(marked(segments('kernel-panic', ['panic']))).toEqual(['panic']);
	});

	it('ignores case and diacritics', () => {
		expect(marked(segments('CAFÉ au café', highlightTerms('cafe')))).toEqual(['CAFÉ', 'café']);
		expect(marked(segments('naïve', highlightTerms('NAIVE')))).toEqual(['naïve']);
	});

	it('marks every term', () => {
		expect(marked(segments('Go 1.26.8 security release', highlightTerms('security go')))).toEqual(['Go', 'security']);
	});

	it('handles no terms, no text and no match', () => {
		expect(segments('abc', [])).toEqual([{ text: 'abc', match: false }]);
		expect(segments('', ['a'])).toEqual([]);
		expect(segments('abc def', ['x'])).toEqual([{ text: 'abc def', match: false }]);
	});

	it('keeps markup as text: nothing is interpreted', () => {
		const s = segments('<b>bold</b> & <script>x</script>', ['bold', 'script']);
		expect(join(s)).toBe('<b>bold</b> & <script>x</script>');
		expect(marked(s)).toEqual(['bold', 'script', 'script']);
	});

	it('works with characters outside the BMP', () => {
		expect(marked(segments('😀 rust 😀', ['rust']))).toEqual(['rust']);
		expect(join(segments('𝒜 rust 日本語', ['rust']))).toBe('𝒜 rust 日本語');
	});
});

describe('segmentsAt', () => {
	it('marks the given character positions', () => {
		expect(segmentsAt('Hacker News', [0, 7])).toEqual([
			{ text: 'H', match: true },
			{ text: 'acker ', match: false },
			{ text: 'N', match: true },
			{ text: 'ews', match: false }
		]);
		expect(segmentsAt('abc', [])).toEqual([{ text: 'abc', match: false }]);
		expect(segmentsAt('', [])).toEqual([]);
		expect(segmentsAt('ab', [0, 1])).toEqual([{ text: 'ab', match: true }]);
	});
});
