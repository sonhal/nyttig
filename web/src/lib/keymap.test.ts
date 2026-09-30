import { describe, expect, it } from 'vitest';
import { keyAction } from './keymap';

describe('keyAction in normal mode', () => {
	it.each([
		['j', { type: 'move', by: 1 }],
		['ArrowDown', { type: 'move', by: 1 }],
		['k', { type: 'move', by: -1 }],
		['ArrowUp', { type: 'move', by: -1 }],
		['g', { type: 'top' }],
		['Home', { type: 'top' }],
		['G', { type: 'bottom' }],
		['End', { type: 'bottom' }],
		['d', { type: 'halfPage', dir: 1 }],
		['u', { type: 'halfPage', dir: -1 }],
		['/', { type: 'focusSearch' }],
		['s', { type: 'cycleSource' }],
		['t', { type: 'cycleTag' }],
		['o', { type: 'toggleSort' }],
		['r', { type: 'refreshAll' }],
		['R', { type: 'refreshSource' }],
		['Enter', { type: 'open' }],
		[' ', { type: 'toggleExpand' }],
		['l', { type: 'toggleExpand' }],
		['q', { type: 'close' }],
		['Escape', { type: 'close' }]
	])('%j', (key, want) => {
		expect(keyAction('normal', { key })).toEqual(want);
	});

	it('supports Ctrl+d/u where the browser passes them through', () => {
		expect(keyAction('normal', { key: 'd', ctrlKey: true })).toEqual({ type: 'halfPage', dir: 1 });
		expect(keyAction('normal', { key: 'u', ctrlKey: true })).toEqual({ type: 'halfPage', dir: -1 });
	});

	it('leaves browser shortcuts and unknown keys alone', () => {
		expect(keyAction('normal', { key: 'r', ctrlKey: true })).toBeNull();
		expect(keyAction('normal', { key: 'j', metaKey: true })).toBeNull();
		expect(keyAction('normal', { key: 'd', ctrlKey: true, altKey: true })).toBeNull();
		expect(keyAction('normal', { key: 'l', altKey: true })).toBeNull();
		expect(keyAction('normal', { key: 'x' })).toBeNull();
		expect(keyAction('normal', { key: 'Tab' })).toBeNull();
		expect(keyAction('normal', { key: 'j', isComposing: true })).toBeNull();
	});
});

describe('keyAction in search mode', () => {
	it('only intercepts Enter and Escape', () => {
		expect(keyAction('search', { key: 'Enter' })).toEqual({ type: 'applySearch' });
		expect(keyAction('search', { key: 'Escape' })).toEqual({ type: 'clearSearch' });
		for (const key of ['j', 'k', 'g', '/', 's', 'q', ' ', 'ArrowDown']) {
			expect(keyAction('search', { key })).toBeNull();
		}
	});
});

describe('keyAction in sheet mode', () => {
	it('only intercepts Escape', () => {
		expect(keyAction('sheet', { key: 'Escape' })).toEqual({ type: 'close' });
		expect(keyAction('sheet', { key: 'j' })).toBeNull();
		expect(keyAction('sheet', { key: 'Enter' })).toBeNull();
	});
});
