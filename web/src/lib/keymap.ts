// Keyboard handling for the feed, as a pure mode machine: one global
// keydown listener asks keyAction() what a key means in the current mode.
//
// Modes:
//   normal  the feed has focus
//   search  the query input has focus; only Enter and Esc are intercepted,
//           everything else is typing
//   sheet   the mobile filter sheet is open; only Esc is intercepted

export type Mode = 'normal' | 'search' | 'sheet';

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
	| { type: 'close' };

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
	Escape: { type: 'close' }
};

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

	// Ctrl+d/u like the TUI, where the browser lets us have them (Ctrl+d
	// bookmarks in some browsers, which wins before the page sees it).
	if (e.ctrlKey && !e.metaKey && !e.altKey) {
		if (e.key === 'd') return { type: 'halfPage', dir: 1 };
		if (e.key === 'u') return { type: 'halfPage', dir: -1 };
		return null;
	}
	// Leave browser and OS shortcuts alone.
	if (e.ctrlKey || e.metaKey || e.altKey) return null;
	return normalKeys[e.key] ?? null;
}
