// Types mirroring the protojson encoding nyttig-api produces
// (UseProtoNames). protojson leaves out zero values, so every field is
// optional, and int64 values (all IDs) are strings: treat them as opaque.

export interface Tag {
	id?: string;
	name?: string;
	color?: string;
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

export type Sort = 'newest' | 'oldest';

/** The feed filter: mirrors the query parameters of /api/stream and /api/items. */
export interface Filter {
	q: string;
	/** Source ID, "" = all. */
	source: string;
	/** Tag ID, "" = all. */
	tag: string;
	sort: Sort;
	unviewed: boolean;
}
