import { describe, expect, it } from 'vitest';
import { History } from './history';

describe('History', () => {
	it('goes back through commands and forward to the typed draft', () => {
		const h = new History();
		h.push('sort oldest');
		h.push('tag linux');
		h.push('help');
		expect(h.prev('par')).toBe('help');
		expect(h.prev('par')).toBe('tag linux');
		expect(h.prev('par')).toBe('sort oldest');
		// At the oldest entry it stays.
		expect(h.prev('par')).toBe('sort oldest');
		expect(h.next()).toBe('tag linux');
		expect(h.next()).toBe('help');
		expect(h.next()).toBe('par');
		expect(h.next()).toBeNull();
	});

	it('has nothing to go back to when empty, and ignores blanks and repeats', () => {
		const h = new History();
		expect(h.prev('x')).toBeNull();
		h.push('  ');
		h.push('help');
		h.push(' help ');
		expect(h.prev('')).toBe('help');
		expect(h.prev('')).toBe('help');
	});

	it('starts again from the newest after a reset', () => {
		const h = new History();
		h.push('a');
		h.push('b');
		h.prev('');
		h.prev('');
		h.reset();
		expect(h.next()).toBeNull();
		expect(h.prev('')).toBe('b');
	});

	it('keeps the last fifty', () => {
		const h = new History();
		for (let i = 0; i < 60; i++) h.push('c' + i);
		let last = '';
		for (let i = 0; i < 100; i++) last = h.prev('') ?? last;
		expect(last).toBe('c10');
	});
});
