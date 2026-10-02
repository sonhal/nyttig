// Rolling time windows for the feed: "since:7d" shows items from the last
// seven days. The same rules as internal/since in Go (the case table in
// since.test.ts is the one in since_test.go): <n><unit>, n 1-9999, units
// h d w mo y in lower case. "m" is rejected as ambiguous. Months and years are
// calendar arithmetic in UTC, with the same end-of-month overflow as Go's
// time.AddDate (31 March - 1mo is 3 March, not 28 February).
//
// The wire carries an absolute cutoff, not the window (see sinceAfter): the
// feed is one stream snapshot plus older pages fetched by offset, and if
// every request counted "now" again the window would slide between them.

export type SinceUnit = 'h' | 'd' | 'w' | 'mo' | 'y';

export interface SinceWindow {
	n: number;
	unit: SinceUnit;
}

/** A valid window as typed. */
export const SINCE_RE = /^([0-9]{1,4})(h|d|w|mo|y)$/;

export const MAX_SINCE = 9999;

/** The windows offered without typing: completion in the query bar. */
export const SINCE_SUGGESTIONS: readonly string[] = ['24h', '7d', '2w', '1mo', '1y'];

/** The windows of the filter sheet's select and the chip's cycle ("" = any time). */
export const SINCE_PRESETS: readonly string[] = ['', '24h', '7d', '30d', '1y'];

export type SinceResult = { ok: true; window: SinceWindow } | { ok: false; error: string };

export function parseSince(text: string): SinceResult {
	const m = SINCE_RE.exec(text);
	if (m) {
		const n = Number(m[1]);
		if (n >= 1) return { ok: true, window: { n, unit: m[2] as SinceUnit } };
		return { ok: false, error: `since: the count must be between 1 and ${MAX_SINCE}, got ${text}` };
	}
	if (text === '') return { ok: false, error: 'since: needs a window such as 24h, 7d, 2w, 1mo or 1y' };
	if (/^[0-9]{1,4}m$/.test(text)) {
		return { ok: false, error: `since: ${text} is ambiguous, use mo for months or h for hours` };
	}
	if (/^[0-9]{5,}[a-z]*$/.test(text)) {
		return { ok: false, error: `since: the count must be between 1 and ${MAX_SINCE}, got ${text}` };
	}
	return { ok: false, error: `since: ${text} is not a window (try 24h, 7d, 2w, 1mo or 1y)` };
}

/** The window as typed, or "" when text is not a valid window (URLs and views are not trusted). */
export function validSince(text: string | undefined): string {
	return text !== undefined && parseSince(text).ok ? text : '';
}

const HOUR = 3600_000;

/** The start of the window that ends at now (ms since the epoch). */
export function cutoff(w: SinceWindow, now: number): number {
	switch (w.unit) {
		case 'h':
			return now - w.n * HOUR;
		case 'd':
			return now - w.n * 24 * HOUR;
		case 'w':
			return now - w.n * 7 * 24 * HOUR;
		case 'mo': {
			const d = new Date(now);
			d.setUTCMonth(d.getUTCMonth() - w.n);
			return d.getTime();
		}
		case 'y': {
			const d = new Date(now);
			d.setUTCFullYear(d.getUTCFullYear() - w.n);
			return d.getTime();
		}
	}
}

/**
 * The absolute cutoff a snapshot sends, in unix seconds, or undefined for no
 * window. It is taken once per snapshot and sent with the stream and with
 * every older page of it. Never before the epoch, which is all the API takes.
 */
export function sinceAfter(since: string, now: number): number | undefined {
	const r = parseSince(since);
	if (!r.ok) return undefined;
	return Math.max(0, Math.floor(cutoff(r.window, now) / 1000));
}

/** The next window in SINCE_PRESETS; a window that is not one of them goes to "any time". */
export function nextSince(current: string): string {
	const i = SINCE_PRESETS.indexOf(current);
	return i < 0 ? '' : (SINCE_PRESETS[(i + 1) % SINCE_PRESETS.length] ?? '');
}

/** "the last 7d" for the empty-feed message. */
export function describeSince(since: string): string {
	return since ? `the last ${since}` : '';
}
