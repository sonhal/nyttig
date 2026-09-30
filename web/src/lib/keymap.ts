// Keyboard handling as a pure mode machine: each view has one global
// keydown listener that asks keyAction() (the feed) or manageKeyAction()
// (sources, tags, rules) what a key means in the current mode.
//
// Feed modes:
//   normal   the feed has focus
//   search   the query input has focus; only Enter and Esc are intercepted,
//            everything else is typing
//   sheet    the mobile filter sheet is open; only Esc is intercepted
//   command  the ":" command line has focus; only Enter and Esc are
//            intercepted
//
// Management modes: normal and command as above, plus
//   form     the add/edit panel is open; only Esc is intercepted (Enter
//            submits the form natively)
//   confirm  a delete confirmation is open: y/Enter confirm, n/Esc/q cancel

export type Mode = 'normal' | 'search' | 'sheet' | 'command';

export type Action =
	| { type: 'move'; by: number }
	| { type: 'halfPage'; dir: 1 | -1 }
	| { type: 'top' }
	| { type: 'bottom' }
	| { type: 'focusSearch' }
	| { type: 'applySearch' }
	| { type: 'clearSearch' }
	| { type: 'cycleSource' }
	| { type: 'cycleTag' }
	| { type: 'toggleSort' }
	| { type: 'refreshAll' }
	| { type: 'refreshSource' }
	| { type: 'open' }
	| { type: 'toggleExpand' }
	| { type: 'close' }
	| CommandAction;

/** Actions shared by every view's command line. */
export type CommandAction = { type: 'openCommand' } | { type: 'runCommand' } | { type: 'cancelCommand' };

/** The parts of a KeyboardEvent the keymap looks at. */
export interface KeyInput {
	key: string;
	ctrlKey?: boolean;
	metaKey?: boolean;
	altKey?: boolean;
	isComposing?: boolean;
}

const normalKeys: Record<string, Action> = {
	j: { type: 'move', by: 1 },
	ArrowDown: { type: 'move', by: 1 },
	k: { type: 'move', by: -1 },
	ArrowUp: { type: 'move', by: -1 },
	g: { type: 'top' },
	Home: { type: 'top' },
	G: { type: 'bottom' },
	End: { type: 'bottom' },
	d: { type: 'halfPage', dir: 1 },
	u: { type: 'halfPage', dir: -1 },
	PageDown: { type: 'halfPage', dir: 1 },
	PageUp: { type: 'halfPage', dir: -1 },
	'/': { type: 'focusSearch' },
	s: { type: 'cycleSource' },
	t: { type: 'cycleTag' },
	o: { type: 'toggleSort' },
	r: { type: 'refreshAll' },
	R: { type: 'refreshSource' },
	Enter: { type: 'open' },
	' ': { type: 'toggleExpand' },
	l: { type: 'toggleExpand' },
	q: { type: 'close' },
	Escape: { type: 'close' },
	':': { type: 'openCommand' }
};

function commandKey(e: KeyInput): CommandAction | null {
	if (e.key === 'Enter') return { type: 'runCommand' };
	if (e.key === 'Escape') return { type: 'cancelCommand' };
	return null;
}

/** Browser and OS shortcuts are never ours. */
function hasModifier(e: KeyInput): boolean {
	return !!(e.ctrlKey || e.metaKey || e.altKey);
}

/**
 * Returns the action for a key in a mode, or null when the key is not ours
 * (the browser then handles it as usual).
 */
export function keyAction(mode: Mode, e: KeyInput): Action | null {
	if (e.isComposing) return null;

	if (mode === 'search') {
		if (e.key === 'Enter') return { type: 'applySearch' };
		if (e.key === 'Escape') return { type: 'clearSearch' };
		return null;
	}
	if (mode === 'sheet') {
		return e.key === 'Escape' ? { type: 'close' } : null;
	}
	if (mode === 'command') return commandKey(e);

	// Ctrl+d/u like the TUI, where the browser lets us have them (Ctrl+d
	// bookmarks in some browsers, which wins before the page sees it).
	if (e.ctrlKey && !e.metaKey && !e.altKey) {
		if (e.key === 'd') return { type: 'halfPage', dir: 1 };
		if (e.key === 'u') return { type: 'halfPage', dir: -1 };
		return null;
	}
	// Leave browser and OS shortcuts alone.
	if (hasModifier(e)) return null;
	return normalKeys[e.key] ?? null;
}

// ── Management views ──────────────────────────────────────────

export type ManageMode = 'normal' | 'command' | 'form' | 'confirm';

export type ManageAction =
	| { type: 'move'; by: number }
	| { type: 'halfPage'; dir: 1 | -1 }
	| { type: 'top' }
	| { type: 'bottom' }
	| { type: 'add' }
	| { type: 'edit' }
	| { type: 'delete' }
	/** Enable or disable the selected source. */
	| { type: 'toggle' }
	/** Fetch the selected source now. */
	| { type: 'refresh' }
	/** Leave the view for the feed. */
	| { type: 'feed' }
	/** Close the form or confirmation. */
	| { type: 'cancel' }
	| { type: 'confirm' }
	| CommandAction;

/** A toolbar button in a management view; each is also a key. */
export interface Tool {
	type: 'add' | 'edit' | 'delete' | 'toggle' | 'refresh';
	label: string;
	/** The key shown in the legend. */
	key: string;
}

const manageKeys: Record<string, ManageAction> = {
	j: { type: 'move', by: 1 },
	ArrowDown: { type: 'move', by: 1 },
	k: { type: 'move', by: -1 },
	ArrowUp: { type: 'move', by: -1 },
	g: { type: 'top' },
	Home: { type: 'top' },
	G: { type: 'bottom' },
	End: { type: 'bottom' },
	d: { type: 'halfPage', dir: 1 },
	u: { type: 'halfPage', dir: -1 },
	PageDown: { type: 'halfPage', dir: 1 },
	PageUp: { type: 'halfPage', dir: -1 },
	a: { type: 'add' },
	e: { type: 'edit' },
	Enter: { type: 'edit' },
	x: { type: 'delete' },
	Delete: { type: 'delete' },
	' ': { type: 'toggle' },
	r: { type: 'refresh' },
	q: { type: 'feed' },
	':': { type: 'openCommand' }
};

/** Like keyAction, for the sources, tags and rules views. */
export function manageKeyAction(mode: ManageMode, e: KeyInput): ManageAction | null {
	if (e.isComposing) return null;
	switch (mode) {
		case 'command':
			return commandKey(e);
		case 'form':
			return e.key === 'Escape' ? { type: 'cancel' } : null;
		case 'confirm':
			if (hasModifier(e)) return null;
			if (e.key === 'y' || e.key === 'Enter') return { type: 'confirm' };
			if (e.key === 'n' || e.key === 'q' || e.key === 'Escape') return { type: 'cancel' };
			return null;
	}
	if (hasModifier(e)) return null;
	return manageKeys[e.key] ?? null;
}
