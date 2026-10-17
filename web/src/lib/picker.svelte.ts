// State of the S/T/v/a fuzzy picker: a list of sources, tags, saved views or assessors, filtered as you
// type. Enter picks the highlighted entry, Esc closes. The C picker (columns)
// is a checklist: its entries carry checked, Enter toggles one and it stays open.

import { fuzzyFilter } from './fuzzy';

export interface PickOption {
	/** The source, tag or view ID; "" is "all". */
	id: string;
	label: string;
	color?: string;
	/** Indentation level, for tags in a tree. */
	depth?: number;
	/** Checklist entries (the column picker): whether it is on. */
	checked?: boolean;
}

export type PickKind = 'source' | 'tag' | 'view' | 'assessor' | 'column';

export class Picker {
	kind: PickKind | null = $state(null);
	query = $state('');
	/** Index into matches of the highlighted entry. */
	active = $state(0);
	options: PickOption[] = $state.raw([]);

	matches = $derived(fuzzyFilter(this.options, this.query, (o) => o.label));

	get open(): boolean {
		return this.kind !== null;
	}

	start(kind: PickKind, options: PickOption[]): void {
		this.kind = kind;
		this.options = options;
		this.query = '';
		this.active = 0;
	}

	close(): void {
		this.kind = null;
	}

	/** New options for the open picker (a checklist after a toggle); the filter text and highlight stay. */
	setOptions(options: PickOption[]): void {
		this.options = options;
	}

	/** Called when the filter text changes: the best match is highlighted again. */
	typed(): void {
		this.active = 0;
	}

	move(by: number): void {
		const n = this.matches.length;
		if (n === 0) return;
		this.active = (((this.active + by) % n) + n) % n;
	}

	/** The highlighted entry, if the filter leaves any. */
	selected(): PickOption | undefined {
		return this.matches[Math.min(this.active, this.matches.length - 1)]?.item;
	}
}
