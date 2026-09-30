import { describe, expect, it } from 'vitest';
import { keyAction, manageKeyAction } from './keymap';

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

describe('the command line', () => {
	it('opens with ":" in the feed and runs on Enter', () => {
		expect(keyAction('normal', { key: ':' })).toEqual({ type: 'openCommand' });
		expect(keyAction('command', { key: 'Enter' })).toEqual({ type: 'runCommand' });
		expect(keyAction('command', { key: 'Escape' })).toEqual({ type: 'cancelCommand' });
		for (const key of ['j', 'q', ' ', 's', ':']) {
			expect(keyAction('command', { key })).toBeNull();
		}
	});

	it('works the same in the management views', () => {
		expect(manageKeyAction('normal', { key: ':' })).toEqual({ type: 'openCommand' });
		expect(manageKeyAction('command', { key: 'Enter' })).toEqual({ type: 'runCommand' });
		expect(manageKeyAction('command', { key: 'Escape' })).toEqual({ type: 'cancelCommand' });
		expect(manageKeyAction('command', { key: 'x' })).toBeNull();
	});
});

describe('manageKeyAction in normal mode', () => {
	it.each([
		['j', { type: 'move', by: 1 }],
		['k', { type: 'move', by: -1 }],
		['ArrowDown', { type: 'move', by: 1 }],
		['g', { type: 'top' }],
		['G', { type: 'bottom' }],
		['d', { type: 'halfPage', dir: 1 }],
		['a', { type: 'add' }],
		['e', { type: 'edit' }],
		['Enter', { type: 'edit' }],
		['x', { type: 'delete' }],
		['Delete', { type: 'delete' }],
		[' ', { type: 'toggle' }],
		['r', { type: 'refresh' }],
		['q', { type: 'feed' }]
	])('%j', (key, want) => {
		expect(manageKeyAction('normal', { key })).toEqual(want);
	});

	it('leaves modified keys and unknown keys alone', () => {
		expect(manageKeyAction('normal', { key: 'r', ctrlKey: true })).toBeNull();
		expect(manageKeyAction('normal', { key: 'x', metaKey: true })).toBeNull();
		expect(manageKeyAction('normal', { key: 'Escape' })).toBeNull();
		expect(manageKeyAction('normal', { key: 'z' })).toBeNull();
		expect(manageKeyAction('normal', { key: 'a', isComposing: true })).toBeNull();
	});
});

describe('manageKeyAction in form mode', () => {
	it('only intercepts Escape; Enter submits the form natively', () => {
		expect(manageKeyAction('form', { key: 'Escape' })).toEqual({ type: 'cancel' });
		for (const key of ['Enter', 'a', 'x', ' ', 'j', 'q', ':']) {
			expect(manageKeyAction('form', { key })).toBeNull();
		}
	});
});

describe('manageKeyAction in confirm mode', () => {
	it('confirms with y or Enter and cancels with n, q or Escape', () => {
		expect(manageKeyAction('confirm', { key: 'y' })).toEqual({ type: 'confirm' });
		expect(manageKeyAction('confirm', { key: 'Enter' })).toEqual({ type: 'confirm' });
		for (const key of ['n', 'q', 'Escape']) {
			expect(manageKeyAction('confirm', { key })).toEqual({ type: 'cancel' });
		}
	});
	it('ignores everything else, so a stray key never deletes', () => {
		for (const key of ['x', 'j', ' ', 'Y', 'a']) {
			expect(manageKeyAction('confirm', { key })).toBeNull();
		}
		expect(manageKeyAction('confirm', { key: 'y', ctrlKey: true })).toBeNull();
	});
});
