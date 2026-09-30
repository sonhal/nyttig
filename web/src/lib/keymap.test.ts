import { describe, expect, it } from 'vitest';
import { feedHelp, keyAction, manageHelp, manageKeyAction, type HelpSection } from './keymap';

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
		['Escape', { type: 'close' }],
		['F', { type: 'follow' }],
		['?', { type: 'openHelp' }],
		['S', { type: 'pickSource' }],
		['T', { type: 'pickTag' }],
		['D', { type: 'toggleTime' }],
		[':', { type: 'openCommand' }]
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
	it('intercepts Enter, Escape and the suggestion keys, and leaves typing alone', () => {
		expect(keyAction('search', { key: 'Enter' })).toEqual({ type: 'applySearch' });
		expect(keyAction('search', { key: 'Escape' })).toEqual({ type: 'clearSearch' });
		expect(keyAction('search', { key: 'Tab' })).toEqual({ type: 'completeQuery' });
		expect(keyAction('search', { key: 'ArrowDown' })).toEqual({ type: 'suggestMove', by: 1 });
		expect(keyAction('search', { key: 'ArrowUp' })).toEqual({ type: 'suggestMove', by: -1 });
		for (const key of ['j', 'k', 'g', '/', 's', 'q', ' ', 'F', '?', ':', 'S']) {
			expect(keyAction('search', { key })).toBeNull();
		}
	});
});

describe('keyAction in help mode', () => {
	it('closes with q, Escape or ?, and nothing else reaches the feed', () => {
		for (const key of ['q', 'Escape', '?']) {
			expect(keyAction('help', { key })).toEqual({ type: 'closeHelp' });
			expect(manageKeyAction('help', { key })).toEqual({ type: 'closeHelp' });
		}
		for (const key of ['j', 'k', 'F', 'S', 'Enter', ' ', ':', 'r', 'x']) {
			expect(keyAction('help', { key })).toBeNull();
			expect(manageKeyAction('help', { key })).toBeNull();
		}
		expect(keyAction('help', { key: 'q', ctrlKey: true })).toBeNull();
	});

	it('opens from the management views too', () => {
		expect(manageKeyAction('normal', { key: '?' })).toEqual({ type: 'openHelp' });
	});
});

describe('keyAction in picker mode', () => {
	it('picks with Enter, closes with Escape and moves with the arrows and Tab', () => {
		expect(keyAction('picker', { key: 'Enter' })).toEqual({ type: 'pickerSelect' });
		expect(keyAction('picker', { key: 'Escape' })).toEqual({ type: 'pickerCancel' });
		expect(keyAction('picker', { key: 'ArrowDown' })).toEqual({ type: 'pickerMove', by: 1 });
		expect(keyAction('picker', { key: 'Tab' })).toEqual({ type: 'pickerMove', by: 1 });
		expect(keyAction('picker', { key: 'ArrowUp' })).toEqual({ type: 'pickerMove', by: -1 });
		// Everything else is typing into the filter.
		for (const key of ['j', 'k', 'q', 's', ' ', 'S', 'F', '?', ':']) {
			expect(keyAction('picker', { key })).toBeNull();
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
		expect(keyAction('command', { key: 'Tab' })).toEqual({ type: 'completeCommand' });
		expect(keyAction('command', { key: 'ArrowUp' })).toEqual({ type: 'historyPrev' });
		expect(keyAction('command', { key: 'ArrowDown' })).toEqual({ type: 'historyNext' });
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

describe('help', () => {
	const find = (sections: HelpSection[], desc: RegExp) =>
		sections.flatMap((s) => s.rows).find((r) => desc.test(r.desc));

	it('lists the feed keys, generated from the tables', () => {
		const help = feedHelp();
		expect(help.map((s) => s.title)).toEqual([
			'Move',
			'Filter',
			'Feed',
			'Views',
			'Query bar',
			'Picker',
			'Command line',
			'Help'
		]);
		expect(find(help, /^down$/)?.keys).toEqual(['j', '↓']);
		expect(find(help, /^half page down$/)?.keys).toEqual(['d', 'PgDn', 'Ctrl+d']);
		expect(find(help, /follow/)?.keys).toEqual(['F']);
		expect(find(help, /expand/)?.keys).toEqual(['Space', 'l']);
		expect(find(help, /^clear the search/)?.keys).toEqual(['Esc']);
		expect(find(help, /^close the help/)?.keys).toEqual(['q', 'Esc', '?']);
	});

	it('drift check: every key the feed handles in normal mode is in the help', () => {
		const listed = new Set(feedHelp().flatMap((s) => s.rows.flatMap((r) => r.keys)));
		const label = (k: string) => ({ ArrowDown: '↓', ArrowUp: '↑', Escape: 'Esc', ' ': 'Space', PageDown: 'PgDn', PageUp: 'PgUp' })[k] ?? k;
		const keys = ['j', 'k', 'g', 'G', 'd', 'u', '/', 's', 't', 'S', 'T', 'o', 'r', 'R', 'F', 'D', 'Enter', ' ', 'l', ':', '?', 'q', 'Escape', 'Home', 'End', 'PageDown', 'PageUp', 'ArrowDown', 'ArrowUp'];
		for (const key of keys) {
			expect(keyAction('normal', { key }), key).not.toBeNull();
			expect(listed.has(label(key)), key).toBe(true);
		}
	});

	it('lists a management view by its tools', () => {
		const all = manageHelp(['add', 'edit', 'delete', 'toggle', 'refresh']);
		expect(find(all, /enable or disable/)?.keys).toEqual(['Space']);
		expect(find(all, /delete the selected/)?.keys).toEqual(['x', 'Del']);
		const tags = manageHelp(['add', 'edit', 'delete']);
		expect(find(tags, /enable or disable/)).toBeUndefined();
		expect(find(tags, /refresh/)).toBeUndefined();
		expect(find(tags, /^add$/)?.keys).toEqual(['a']);
		expect(tags.map((s) => s.title)).toContain('Delete confirmation');
	});
});
