// Per-browser display preferences, kept in localStorage. Storage can be
// missing or throw (private windows, blocked site data), so every access is
// wrapped and the app works without it.

import type { TimeMode } from './command';

const TIME_KEY = 'nyttig.time';

function load(): TimeMode {
	try {
		return localStorage.getItem(TIME_KEY) === 'relative' ? 'relative' : 'absolute';
	} catch {
		return 'absolute';
	}
}

class Prefs {
	/** Dates as "30.09 10:32" (absolute) or "12m ago" (relative). */
	timeMode: TimeMode = $state(load());

	setTime(mode: TimeMode): void {
		this.timeMode = mode;
		try {
			localStorage.setItem(TIME_KEY, mode);
		} catch {
			// Not persisted; it still applies until the page is closed.
		}
	}

	toggleTime(): TimeMode {
		this.setTime(this.timeMode === 'relative' ? 'absolute' : 'relative');
		return this.timeMode;
	}
}

export const prefs = new Prefs();
