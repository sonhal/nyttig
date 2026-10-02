// Types mirroring the protojson encoding nyttig-api produces
// (UseProtoNames). protojson leaves out zero values, so every field is
// optional, and int64 values (all IDs) are strings: treat them as opaque.

export interface Tag {
	id?: string;
	name?: string;
	color?: string;
	/** Direct parents; absent for a top-level tag. */
	parent_ids?: string[];
}

export interface Item {
	id: string;
	source_id?: string;
	source_name?: string;
	guid?: string;
	link?: string;
	title?: string;
	description?: string;
	author?: string;
	/** RFC 3339 timestamp. */
	published?: string;
	/** RFC 3339 timestamp. */
	fetched_at?: string;
	tags?: Tag[];
	viewed?: boolean;
	/** Every assessor's assessment of the item, by assessor name. */
	assessments?: Assessment[];
}

/** A system (a model, a CVE reader, you) that scores items. */
export interface Assessor {
	id?: string;
	name?: string;
	/** What the assessor's score means. */
	description?: string;
	color?: string;
	created_at?: string;
}

/**
 * One assessor's judgement on an item: an optional score from 0 to 1 and an
 * optional note, for one tag or for the item as a whole. The note and the
 * assessor's name are untrusted text (an LLM's note can repeat the markup or
 * instructions of the feed it read): render them as text only.
 */
export interface Assessment {
	id?: string;
	item_id?: string;
	assessor_id?: string;
	assessor_name?: string;
	/** Absent for the item as a whole. */
	tag_id?: string;
	/** Absent for no score; 0 is a score. */
	score?: number;
	note?: string;
	/** RFC 3339 timestamp. */
	updated_at?: string;
}

export interface Source {
	id?: string;
	name?: string;
	url?: string;
	type?: string;
	refresh_sec?: number;
	enabled?: boolean;
	created_at?: string;
	last_fetch?: string;
	fetch_error?: string;
	color?: string;
	abbreviation?: string;
}

export type Sort = 'newest' | 'oldest' | 'score';

/** The feed filter: mirrors the query parameters of /api/stream and /api/items. */
export interface Filter {
	q: string;
	/** Source ID, "" = all. */
	source: string;
	/** Tag ID, "" = all. */
	tag: string;
	sort: Sort;
	unviewed: boolean;
	/** A rolling window such as "7d" (since.ts), "" = any time. */
	since: string;
	/** Assessor ID, "" = none: whose scores minScore and sort "score" use, and which is shown first. */
	assessor: string;
	/** Only items the assessor scored at least this (0 to 1); null = no minimum. Needs assessor. */
	minScore: number | null;
	/** Assessor ID, "" = none: only items this assessor has not assessed. */
	unassessed: string;
}

export type RuleField = 'title' | 'description' | 'both';

export interface TagRule {
	id?: string;
	/** Absent (0) for a global rule. */
	source_id?: string;
	tag_id?: string;
	tag_name?: string;
	field?: string;
	pattern?: string;
	priority?: number;
}

/** POST /api/rules/test: a dry run of a pattern against recent items. */
export interface RuleTest {
	/** Matching items, newest first. */
	items: Item[];
	/** How many recent items the daemon checked. */
	scanned: number;
}

/**
 * A saved view's filter as nyttig-api sends and takes it: the keys of
 * Filter, with zero values left out. It is always present on a view (it can
 * be {}), and IDs are strings.
 */
export interface ViewFilter {
	q?: string;
	source?: string;
	tag?: string;
	sort?: string;
	unviewed?: boolean;
	/** The window as the user typed it ("7d"); absent = any time. */
	since?: string;
	assessor?: string;
	min_score?: number;
	unassessed?: string;
}

/** A saved view. Unlike the other types this is not protojson: see viewJSON in internal/api/manage.go. */
export interface SavedView {
	id?: string;
	name?: string;
	filter?: ViewFilter;
	favorite?: boolean;
	/** The display order of the views, from 0. */
	position?: number;
}
