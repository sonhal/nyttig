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
