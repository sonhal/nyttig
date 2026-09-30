import { describe, expect, it } from 'vitest';
import { fuzzyFilter, fuzzyScore } from './fuzzy';

describe('fuzzyScore', () => {
	it('needs the characters in order, ignoring case', () => {
		expect(fuzzyScore('Hacker News', 'hn')?.at).toEqual([0, 7]);
		expect(fuzzyScore('Hacker News', 'nh')).toBeNull();
		expect(fuzzyScore('Hacker News', 'xyz')).toBeNull();
		expect(fuzzyScore('Hacker News', '')).toEqual({ at: [], score: 0 });
		expect(fuzzyScore('Hacker News', 'h n')?.at).toEqual([0, 7]);
	});

	it('prefers word starts over letters inside a word', () => {
		expect(fuzzyScore('Hacker News', 'n')?.at).toEqual([7]);
		expect(fuzzyScore('Release notes', 'n')?.at).toEqual([8]);
	});
});

describe('fuzzyFilter', () => {
	const names = ['Go Blog', 'Hacker News', 'Alpha News', 'LWN.net', 'Rust Blog'];
	const f = (q: string) => fuzzyFilter(names, q, (x) => x).map((m) => m.item);

	it('keeps everything in order for an empty query', () => {
		expect(f('')).toEqual(names);
	});

	it('drops what does not match and ranks the rest', () => {
		expect(f('news')).toEqual(['Hacker News', 'Alpha News']);
		expect(f('blog')).toEqual(['Go Blog', 'Rust Blog']);
		expect(f('zzz')).toEqual([]);
	});

	it('ranks prefixes and runs first', () => {
		expect(f('ru')[0]).toBe('Rust Blog');
		expect(f('go')[0]).toBe('Go Blog');
		expect(f('lwn')[0]).toBe('LWN.net');
	});

	it('reports the matched positions', () => {
		const [m] = fuzzyFilter(names, 'hn', (x) => x);
		expect(m?.item).toBe('Hacker News');
		expect(m?.at).toEqual([0, 7]);
	});
});
