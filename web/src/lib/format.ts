// Display formatting shared by the feed views.

function pad(n: number): string {
	return n < 10 ? '0' + n : String(n);
}

/** "dd.MM HH:mm" in local time, like the TUI's date column; "" if unknown. */
export function formatDate(ts: string | undefined): string {
	if (!ts) return '';
	const d = new Date(ts);
	if (Number.isNaN(d.getTime())) return '';
	return `${pad(d.getDate())}.${pad(d.getMonth() + 1)} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** "HH:mm" in local time (the compact mobile column). */
export function formatTime(ts: string | undefined): string {
	if (!ts) return '';
	const d = new Date(ts);
	if (Number.isNaN(d.getTime())) return '';
	return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** A short duration such as "45s", "12m" or "3h"; ms may be negative. */
export function shortDuration(ms: number): string {
	const s = Math.max(0, Math.round(ms / 1000));
	if (s < 60) return `${s}s`;
	if (s < 3600) return `${Math.floor(s / 60)}m`;
	if (s < 86400) return `${Math.floor(s / 3600)}h`;
	return `${Math.floor(s / 86400)}d`;
}

/**
 * How long ago a time was: "45s ago", "12m ago", "3h ago", "2d ago", or
 * "in 5m" for a time ahead of now (a feed with a wrong clock). short drops
 * the "ago", for the narrow mobile column. "" if unknown.
 */
export function formatRelative(ts: string | undefined, now: number, short = false): string {
	if (!ts) return '';
	const t = new Date(ts).getTime();
	if (Number.isNaN(t)) return '';
	const diff = now - t;
	// A minute of slack for clocks that disagree.
	if (diff < -60_000) return 'in ' + shortDuration(-diff);
	return shortDuration(diff) + (short ? '' : ' ago');
}
