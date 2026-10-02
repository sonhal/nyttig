// Keyboard handling as a pure mode machine: each view has one global
// keydown listener that asks keyAction() (the feed) or manageKeyAction()
// (sources, tags, rules, views) what a key means in the current mode.
//
// Every mode's keys live in a table of bindings (BINDINGS below). The key
// handler looks keys up in those tables and the help overlay (help.ts) is
// generated from the same tables, so the two cannot drift apart.
//
// Feed modes:
//   normal   the feed has focus
//   search   the query input has focus; only the keys in SEARCH are
//            intercepted, everything else is typing
//   sheet    the mobile filter sheet is open; only Esc is intercepted
//   command  the ":" command line has focus; only COMMAND's keys are
//            intercepted
//   picker   the S/T/v fuzzy picker has focus (its input takes the typing)
//   help     the help overlay is open; q, Esc and ? close it
//
// Management modes: normal, command and help as above, plus
//   form     the add/edit panel is open; only Esc is intercepted (Enter
//            submits the form natively)
//   confirm  a delete confirmation is open: y/Enter confirm, n/Esc/q cancel

export type Mode = 'normal' | 'search' | 'sheet' | 'command' | 'picker' | 'help';

export type Action =
	| { type: 'move'; by: number }
	| { type: 'halfPage'; dir: 1 | -1 }
	| { type: 'top' }
	| { type: 'bottom' }
	| { type: 'focusSearch' }
	| { type: 'applySearch' }
	| { type: 'clearSearch' }
	/** Tab in the query bar: accept a name suggestion. */
	| { type: 'completeQuery' }
	/** Up/Down in the query bar: move through the suggestions. */
	| { type: 'suggestMove'; by: number }
	| { type: 'cycleSource' }
	| { type: 'cycleTag' }
	| { type: 'pickSource' }
	| { type: 'pickTag' }
	/** 1-9: the nth favorite view; 0: the unfiltered feed. */
	| { type: 'viewTab'; n: number }
	| { type: 'viewPicker' }
	| { type: 'pickerMove'; by: number }
	| { type: 'pickerSelect' }
	| { type: 'pickerCancel' }
	| { type: 'toggleSort' }
	| { type: 'toggleTime' }
	| { type: 'follow' }
	| { type: 'refreshAll' }
	| { type: 'refreshSource' }
	| { type: 'open' }
	| { type: 'toggleExpand' }
	| { type: 'close' }
	| HelpAction
	| CommandAction;

/** Actions shared by every view's command line. */
export type CommandAction =
	| { type: 'openCommand' }
	| { type: 'runCommand' }
	| { type: 'cancelCommand' }
	| { type: 'completeCommand' }
	| { type: 'historyPrev' }
	| { type: 'historyNext' };

/** Actions shared by every view's help overlay. */
export type HelpAction = { type: 'openHelp' } | { type: 'closeHelp' };

/** The parts of a KeyboardEvent the keymap looks at. */
export interface KeyInput {
	key: string;
	ctrlKey?: boolean;
	metaKey?: boolean;
	altKey?: boolean;
	isComposing?: boolean;
}

/** One key binding: the keys, what they do, and how the help describes them. */
export interface Binding<A> {
	keys: readonly string[];
	/** With Ctrl held (Ctrl+d/u, where the browser lets us have them). */
	ctrl?: boolean;
	action: A;
	desc: string;
	/** The help section; bindings with the same section are listed together. */
	group: string;
	/** Management views: only when the view has this tool. */
	tool?: Tool['type'];
}

type FeedBinding = Binding<Action>;

const b = <A>(group: string, keys: string[], action: A, desc: string, extra: Partial<Binding<A>> = {}): Binding<A> => ({
	keys,
	action,
	desc,
	group,
	...extra
});

// ── Feed tables ───────────────────────────────────────────────

const NORMAL: FeedBinding[] = [
	b('Move', ['j', 'ArrowDown'], { type: 'move', by: 1 }, 'down'),
	b('Move', ['k', 'ArrowUp'], { type: 'move', by: -1 }, 'up'),
	b('Move', ['g', 'Home'], { type: 'top' }, 'first item'),
	b('Move', ['G', 'End'], { type: 'bottom' }, 'last item'),
	b('Move', ['d', 'PageDown'], { type: 'halfPage', dir: 1 }, 'half page down'),
	b('Move', ['u', 'PageUp'], { type: 'halfPage', dir: -1 }, 'half page up'),
	b('Move', ['d'], { type: 'halfPage', dir: 1 }, 'half page down', { ctrl: true }),
	b('Move', ['u'], { type: 'halfPage', dir: -1 }, 'half page up', { ctrl: true }),
	b('Filter', ['/'], { type: 'focusSearch' }, 'search and filter (tag:, src:, is:, sort:)'),
	b('Filter', ['s'], { type: 'cycleSource' }, 'cycle the source'),
	b('Filter', ['t'], { type: 'cycleTag' }, 'cycle the tag'),
	b('Filter', ['S'], { type: 'pickSource' }, 'pick a source by name'),
	b('Filter', ['T'], { type: 'pickTag' }, 'pick a tag by name'),
	b('Filter', ['o'], { type: 'toggleSort' }, 'toggle newest/oldest first'),
	b('Saved views', ['1'], { type: 'viewTab', n: 1 }, 'go to favorite view 1-9'),
	b('Saved views', ['2'], { type: 'viewTab', n: 2 }, 'go to favorite view 1-9'),
	b('Saved views', ['3'], { type: 'viewTab', n: 3 }, 'go to favorite view 1-9'),
	b('Saved views', ['4'], { type: 'viewTab', n: 4 }, 'go to favorite view 1-9'),
	b('Saved views', ['5'], { type: 'viewTab', n: 5 }, 'go to favorite view 1-9'),
	b('Saved views', ['6'], { type: 'viewTab', n: 6 }, 'go to favorite view 1-9'),
	b('Saved views', ['7'], { type: 'viewTab', n: 7 }, 'go to favorite view 1-9'),
	b('Saved views', ['8'], { type: 'viewTab', n: 8 }, 'go to favorite view 1-9'),
	b('Saved views', ['9'], { type: 'viewTab', n: 9 }, 'go to favorite view 1-9'),
	b('Saved views', ['0'], { type: 'viewTab', n: 0 }, 'the unfiltered feed'),
	b('Saved views', ['v'], { type: 'viewPicker' }, 'pick a saved view by name'),
	b('Feed', ['F'], { type: 'follow' }, 'follow: jump to the newest and stick to it'),
	b('Feed', ['r'], { type: 'refreshAll' }, 'refresh all sources'),
	b('Feed', ['R'], { type: 'refreshSource' }, "refresh the selected item's source"),
	b('Feed', ['Enter'], { type: 'open' }, 'open the link in a new tab'),
	b('Feed', [' ', 'l'], { type: 'toggleExpand' }, 'expand or collapse the row'),
	b('Feed', ['D'], { type: 'toggleTime' }, 'relative or absolute times'),
	b('General', [':'], { type: 'openCommand' }, 'command line'),
	b('General', ['?'], { type: 'openHelp' }, 'this help'),
	b('General', ['q', 'Escape'], { type: 'close' }, 'close the expanded row')
];

const SEARCH: FeedBinding[] = [
	b('Query bar', ['Enter'], { type: 'applySearch' }, 'apply the query (or accept the highlighted suggestion)'),
	b('Query bar', ['Escape'], { type: 'clearSearch' }, 'clear the search text and leave the bar'),
	b('Query bar', ['Tab'], { type: 'completeQuery' }, 'complete a tag or source name'),
	b('Query bar', ['ArrowDown'], { type: 'suggestMove', by: 1 }, 'next suggestion'),
	b('Query bar', ['ArrowUp'], { type: 'suggestMove', by: -1 }, 'previous suggestion')
];

const SHEET: FeedBinding[] = [b('Filters', ['Escape'], { type: 'close' }, 'close the filter sheet')];

const COMMAND: Binding<CommandAction>[] = [
	b('Command line', ['Enter'], { type: 'runCommand' }, 'run the command'),
	b('Command line', ['Escape'], { type: 'cancelCommand' }, 'cancel'),
	b('Command line', ['Tab'], { type: 'completeCommand' }, 'complete (again to cycle)'),
	b('Command line', ['ArrowUp'], { type: 'historyPrev' }, 'previous command'),
	b('Command line', ['ArrowDown'], { type: 'historyNext' }, 'next command')
];

const PICKER: FeedBinding[] = [
	b('Picker', ['Enter'], { type: 'pickerSelect' }, 'choose the highlighted entry'),
	b('Picker', ['Escape'], { type: 'pickerCancel' }, 'close'),
	b('Picker', ['ArrowDown', 'Tab'], { type: 'pickerMove', by: 1 }, 'next entry'),
	b('Picker', ['ArrowUp'], { type: 'pickerMove', by: -1 }, 'previous entry')
];

const HELP: Binding<HelpAction>[] = [b('Help', ['q', 'Escape', '?'], { type: 'closeHelp' }, 'close the help')];

/** Browser and OS shortcuts are never ours. */
function hasModifier(e: KeyInput): boolean {
	return !!(e.ctrlKey || e.metaKey || e.altKey);
}

/** The action for a key in a table, or null. Ctrl bindings need Ctrl alone. */
function lookup<A>(table: readonly Binding<A>[], e: KeyInput): A | null {
	const ctrl = !!e.ctrlKey && !e.metaKey && !e.altKey;
	if (!ctrl && hasModifier(e)) return null;
	for (const bd of table) {
		if (!!bd.ctrl === ctrl && bd.keys.includes(e.key)) return bd.action;
	}
	return null;
}

/**
 * Returns the action for a key in a mode, or null when the key is not ours
 * (the browser then handles it as usual).
 */
export function keyAction(mode: Mode, e: KeyInput): Action | null {
	if (e.isComposing) return null;
	switch (mode) {
		case 'search':
			return lookup(SEARCH, e);
		case 'sheet':
			return lookup(SHEET, e);
		case 'command':
			return lookup(COMMAND, e);
		case 'picker':
			return lookup(PICKER, e);
		case 'help':
			return lookup(HELP, e);
	}
	return lookup(NORMAL, e);
}

// ── Management views ──────────────────────────────────────────

export type ManageMode = 'normal' | 'command' | 'form' | 'confirm' | 'help';

export type ManageAction =
	| { type: 'move'; by: number }
	| { type: 'halfPage'; dir: 1 | -1 }
	| { type: 'top' }
	| { type: 'bottom' }
	| { type: 'add' }
	| { type: 'edit' }
	| { type: 'delete' }
	/** Enable or disable the selected source; favorite or unfavorite a view. */
	| { type: 'toggle' }
	/** Views: move the selected view one place up or down the list. */
	| { type: 'moveUp' }
	| { type: 'moveDown' }
	/** Fetch the selected source now. */
	| { type: 'refresh' }
	/** Leave the view for the feed. */
	| { type: 'feed' }
	/** Close the form or confirmation. */
	| { type: 'cancel' }
	| { type: 'confirm' }
	| HelpAction
	| CommandAction;

/** A toolbar button in a management view; each is also a key. */
export interface Tool {
	type: 'add' | 'edit' | 'delete' | 'toggle' | 'refresh' | 'moveUp' | 'moveDown';
	label: string;
	/** The key shown in the legend. */
	key: string;
}

type ManageBinding = Binding<ManageAction>;

const MANAGE: ManageBinding[] = [
	b('Move', ['j', 'ArrowDown'], { type: 'move', by: 1 }, 'down'),
	b('Move', ['k', 'ArrowUp'], { type: 'move', by: -1 }, 'up'),
	b('Move', ['g', 'Home'], { type: 'top' }, 'first row'),
	b('Move', ['G', 'End'], { type: 'bottom' }, 'last row'),
	b('Move', ['d', 'PageDown'], { type: 'halfPage', dir: 1 }, 'half page down'),
	b('Move', ['u', 'PageUp'], { type: 'halfPage', dir: -1 }, 'half page up'),
	b('Edit', ['a'], { type: 'add' }, 'add', { tool: 'add' }),
	b('Edit', ['e', 'Enter'], { type: 'edit' }, 'edit the selected row', { tool: 'edit' }),
	b('Edit', ['x', 'Delete'], { type: 'delete' }, 'delete the selected row', { tool: 'delete' }),
	b('Edit', [' '], { type: 'toggle' }, 'enable or disable the selected source, or favorite a view', { tool: 'toggle' }),
	b('Edit', ['K'], { type: 'moveUp' }, 'move the selected view up', { tool: 'moveUp' }),
	b('Edit', ['J'], { type: 'moveDown' }, 'move the selected view down', { tool: 'moveDown' }),
	b('Edit', ['r'], { type: 'refresh' }, 'refresh the selected source now', { tool: 'refresh' }),
	b('General', ['q'], { type: 'feed' }, 'back to the feed'),
	b('General', [':'], { type: 'openCommand' }, 'command line'),
	b('General', ['?'], { type: 'openHelp' }, 'this help')
];

const FORM: ManageBinding[] = [b('Form', ['Escape'], { type: 'cancel' }, 'close the form without saving')];

const CONFIRM: ManageBinding[] = [
	b('Delete confirmation', ['y', 'Enter'], { type: 'confirm' }, 'delete'),
	b('Delete confirmation', ['n', 'q', 'Escape'], { type: 'cancel' }, 'keep it')
];

/** Like keyAction, for the sources, tags and rules views. */
export function manageKeyAction(mode: ManageMode, e: KeyInput): ManageAction | null {
	if (e.isComposing) return null;
	switch (mode) {
		case 'command':
			return lookup(COMMAND, e);
		case 'form':
			return lookup(FORM, e);
		case 'confirm':
			return lookup(CONFIRM, e);
		case 'help':
			return lookup(HELP, e);
	}
	return lookup(MANAGE, e);
}

// ── Help ──────────────────────────────────────────────────────

export interface HelpRow {
	/** Key labels, e.g. ["j", "↓"]. */
	keys: string[];
	desc: string;
}

export interface HelpSection {
	title: string;
	rows: HelpRow[];
}

const KEY_LABELS: Record<string, string> = {
	ArrowDown: '↓',
	ArrowUp: '↑',
	ArrowLeft: '←',
	ArrowRight: '→',
	Escape: 'Esc',
	' ': 'Space',
	PageDown: 'PgDn',
	PageUp: 'PgUp',
	Delete: 'Del'
};

function keyLabel(key: string, ctrl: boolean | undefined): string {
	const k = KEY_LABELS[key] ?? key;
	return ctrl ? 'Ctrl+' + k : k;
}

/** Groups bindings into help sections, in table order. */
function sections<A>(tables: readonly (readonly Binding<A>[])[], keep: (bd: Binding<A>) => boolean): HelpSection[] {
	const out: HelpSection[] = [];
	for (const table of tables) {
		for (const bd of table) {
			if (!keep(bd)) continue;
			let s = out.find((x) => x.title === bd.group);
			if (!s) out.push((s = { title: bd.group, rows: [] }));
			const keys = bd.keys.map((k) => keyLabel(k, bd.ctrl));
			// Bindings of one description (d and Ctrl+d) share a row.
			const row = s.rows.find((r) => r.desc === bd.desc);
			if (row) row.keys.push(...keys);
			else s.rows.push({ keys, desc: bd.desc });
		}
	}
	return out;
}

/** Every key of the feed, by mode: the source of the help overlay. */
export function feedHelp(): HelpSection[] {
	return sections<Action>([NORMAL, SEARCH, PICKER, COMMAND, HELP], () => true);
}

/** Every key of a management view that has the given tools. */
export function manageHelp(tools: readonly Tool['type'][]): HelpSection[] {
	const has = (bd: Binding<ManageAction>) => !bd.tool || tools.includes(bd.tool);
	return sections<ManageAction>([MANAGE, FORM, CONFIRM, COMMAND, HELP], has);
}
