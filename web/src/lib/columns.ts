// The feed's columns, which a filter can show or hide: "cols:title,source"
// in the "/" bar, cols= in the URL, cols in a saved view. The same rules as
// internal/columns in Go (the case table in columns.test.ts is the one in
// columns_test.go): canonical names in display order, the full set (or
// nothing) means every column. The unviewed dot is not a column.
//
// Columns only change how rows look: they never reach /api/items or
// /api/stream (filter.ts: apiParams).

export type Column = 'date' | 'source' | 'tags' | 'score' | 'title' | 'desc' | 'domain';

/** Every column in display order, with its label for the picker. */
export const COLUMNS: readonly { name: Column; label: string }[] = [
	{ name: 'date', label: 'date' },
	{ name: 'source', label: 'source' },
	{ name: 'tags', label: 'tags' },
	{ name: 'score', label: 'scores' },
	{ name: 'title', label: 'title' },
	{ name: 'desc', label: 'description' },
	{ name: 'domain', label: 'domain' }
];

const ORDER: readonly Column[] = COLUMNS.map((c) => c.name);

const ALIASES: Record<string, Column> = {
	time: 'date',
	src: 'source',
	tag: 'tags',
	scores: 'score',
	description: 'desc'
};

export type ColumnsResult = { ok: true; columns: Column[] } | { ok: false; error: string };

const isColumn = (s: string): s is Column => (ORDER as readonly string[]).includes(s);

/**
 * Canonical names in display order, [] for the full set. Unknown names,
 * empty names and duplicates are errors.
 */
export function normalizeColumns(names: readonly string[]): ColumnsResult {
	const seen = new Set<Column>();
	for (const n of names) {
		if (n === '') return { ok: false, error: 'cols: empty column name' };
		if (!isColumn(n)) return { ok: false, error: `cols: unknown column ${n} (${ORDER.join(', ')})` };
		if (seen.has(n)) return { ok: false, error: `cols: column ${n} given twice` };
		seen.add(n);
	}
	const columns = ORDER.filter((c) => seen.has(c));
	return { ok: true, columns: columns.length === ORDER.length ? [] : columns };
}

/**
 * Reads "title, src": trimmed, case-insensitive, aliases accepted; "" is every
 * column. A list whose names all start with "-" hides them instead
 * ("-desc,-domain" is every column but those); mixing the two, or hiding every
 * column, is an error. The result is always the shown columns.
 */
export function parseColumns(text: string): ColumnsResult {
	if (text.trim() === '') return { ok: true, columns: [] };
	let hidden = 0;
	const names = text.split(',').map((p) => {
		let n = p.trim().toLowerCase();
		if (n.startsWith('-')) {
			hidden++;
			n = n.slice(1).trim();
		}
		return ALIASES[n] ?? n;
	});
	if (hidden === 0) return normalizeColumns(names);
	if (hidden !== names.length) return { ok: false, error: "cols: don't mix shown and hidden (-name) columns" };
	const checked = normalizeColumns(names);
	if (!checked.ok) return checked;
	const shown = ORDER.filter((c) => !names.includes(c));
	if (shown.length === 0) return { ok: false, error: "cols: can't hide every column" };
	return normalizeColumns(shown);
}

/**
 * The known columns of a stored list, in display order; anything else is
 * dropped rather than refused, so a view saved by a newer client still opens.
 */
export function knownColumns(names: readonly string[] | undefined): Column[] {
	const r = normalizeColumns([...new Set(names ?? [])].filter(isColumn));
	return r.ok ? r.columns : [];
}

/** The selection as the URL and the API carry it: the shown columns. */
export function formatColumns(columns: readonly Column[]): string {
	return columns.join(',');
}

/**
 * The selection the shorter way for people to read: the shown columns
 * ("source,title"), or the hidden ones when fewer ("-desc,-domain").
 * parseColumns reads both back.
 */
export function spellColumns(columns: readonly Column[]): string {
	if (columns.length === 0 || 2 * columns.length <= ORDER.length) return formatColumns(columns);
	return ORDER.filter((c) => !columns.includes(c))
		.map((c) => '-' + c)
		.join(',');
}

/**
 * Completion of a column list: the names for the part after the last comma,
 * leaving out those already in the list. A part starting with "-" gets "-"
 * names. offset is where that part's name starts in the list.
 */
export function columnCandidates(list: string): { offset: number; candidates: string[] } {
	const comma = list.lastIndexOf(',');
	const used = list
		.slice(0, comma + 1)
		.split(',')
		.map((x) => x.trim().replace(/^-\s*/, '').toLowerCase());
	const part = list.slice(comma + 1);
	const word = part.trimStart();
	const minus = word.startsWith('-') ? '-' : '';
	const typed = word.slice(minus.length).toLowerCase();
	const names = ORDER.filter((n) => !used.includes(n));
	const starts = names.filter((n) => n.startsWith(typed));
	const contains = names.filter((n) => !n.startsWith(typed) && n.includes(typed));
	return { offset: comma + 1 + (part.length - word.length), candidates: [...starts, ...contains].map((n) => minus + n) };
}

/** Whether a row shows the column: [] shows everything. */
export function shows(columns: readonly Column[], name: Column): boolean {
	return columns.length === 0 || columns.includes(name);
}

/**
 * The selection with one column switched. Switching off the last shown column
 * is refused (null): a row needs something in it.
 */
export function toggleColumn(columns: readonly Column[], name: Column): Column[] | null {
	const shown = columns.length === 0 ? [...ORDER] : [...columns];
	const next = shown.includes(name) ? shown.filter((c) => c !== name) : [...shown, name];
	if (next.length === 0) return null;
	const r = normalizeColumns(next);
	return r.ok ? r.columns : null;
}

/** The column names to complete after "cols:" and after each comma. */
export const COLUMN_NAMES: readonly Column[] = ORDER;
