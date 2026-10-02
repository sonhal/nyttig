// State of the ":" command line, shared by the feed and the management
// views, running commands, and navigation between views.

import { goto } from '$app/navigation';
import * as api from './api';
import {
	applyToFilter,
	COMMANDS,
	commonPrefix,
	completeCommand,
	parseCommand,
	PAGE_PATHS,
	type Command,
	type CommandContext,
	type Page
} from './command';
import { filterFromParams } from './filter';
import { History } from './history';
import { metadata } from './metadata.svelte';
import { prefs } from './prefs.svelte';
import { oneLine } from './sanitize';
import type { Filter } from './types';
import { filterToViewBody, viewHref, viewSearch } from './views';

/** The URL of a page; the feed keeps the filter it had last. */
export function pageHref(v: Page): string {
	return v === 'feed' ? '/' + metadata.feedSearch : PAGE_PATHS[v];
}

export function goPage(v: Page): Promise<void> {
	return goto(pageHref(v));
}

/** What a page has to provide to run commands that are about the page itself. */
export interface CommandHost {
	help(): void;
	/** Shows a message in the view's status area. */
	flash(msg: string): void;
	/** Fetches all sources, or one. */
	refresh(sourceId?: string): void;
	/** The filter the feed shows (or will show). */
	filter(): Filter;
	setFilter(f: Filter): void;
	/** The ID of the saved view the feed has open, or "". */
	activeView(): string;
	follow(): void;
	/** Rates the selected item as the assessor "me" (the feed's command). */
	rate(score: number, note: string): void;
}

/**
 * Runs a parsed command. The feed and the management views share this; a
 * filter command typed in a management view goes to the feed with the filter
 * applied.
 */
export function execute(cmd: Command, host: CommandHost): void {
	switch (cmd.type) {
		case 'page':
			void goPage(cmd.page);
			return;
		case 'view': {
			const v = metadata.views.find((x) => x.id === cmd.id);
			void goto(v ? viewHref(v) : '/');
			return;
		}
		case 'save':
			void saveView(cmd.name, host);
			return;
		case 'help':
			host.help();
			return;
		case 'follow':
			host.follow();
			return;
		case 'rate':
			host.rate(cmd.score, cmd.note);
			return;
		case 'refresh':
			host.refresh(cmd.source || undefined);
			return;
		case 'time': {
			if (cmd.mode === 'toggle') prefs.toggleTime();
			else prefs.setTime(cmd.mode);
			host.flash('times: ' + prefs.timeMode);
			return;
		}
		default: {
			if (cmd.type === 'sort' && cmd.sort === 'score' && !host.filter().assessor) {
				host.flash('error: sort score needs an assessor: :score <name> first');
				return;
			}
			const f = applyToFilter(cmd, host.filter());
			if (f) host.setFilter(f);
		}
	}
}

/**
 * ":save": writes the filter into the active view, or, with a name, creates
 * a favorite view from it and opens it.
 */
async function saveView(name: string, host: CommandHost): Promise<void> {
	const filter = host.filter();
	try {
		if (name === '') {
			const id = host.activeView();
			const view = metadata.views.find((v) => v.id === id);
			if (!view) {
				host.flash('error: no view is open; use :save <name> to create one');
				return;
			}
			await api.updateView(id, { filter: filterToViewBody(filter) });
			await metadata.reloadViews();
			host.flash(`saved ${oneLine(view.name)}`);
			return;
		}
		const created = await api.addView({ name, filter: filterToViewBody(filter), favorite: true });
		await metadata.reloadViews();
		host.flash(`saved as ${oneLine(created.name)}`);
		await goto(viewHref(created));
	} catch (e) {
		host.flash('error: ' + (e instanceof Error ? e.message : String(e)));
	}
}

/** The view the feed has open, as the URL has it (for pages other than the feed). */
export function feedViewId(): string {
	return new URLSearchParams(metadata.feedSearch).get('view') ?? '';
}

/** The feed's current filter as the URL has it (for views other than the feed). */
export function feedFilter(): Filter {
	return filterFromParams(new URLSearchParams(metadata.feedSearch));
}

/** The default CommandHost parts for a view other than the feed. */
export function offFeedHost(flash: (msg: string) => void, refresh: (id?: string) => void): CommandHost {
	return {
		help: () => {},
		flash,
		refresh,
		filter: feedFilter,
		// The view stays open: its tab shows "*" when the filter differs.
		setFilter: (f) => void goto('/' + viewSearch(feedViewId(), f)),
		activeView: feedViewId,
		follow: () => void goPage('feed'),
		rate: () => flash('error: :rate rates the selected item in the feed')
	};
}

// One history for every command line: it survives moving between views.
const history = new History();

interface Cycle {
	from: number;
	candidates: string[];
	index: number;
	/** The text after the last completion, to tell whether the user typed since. */
	text: string;
}

export class CommandLine {
	open = $state(false);
	text = $state('');
	error = $state('');
	/** Completion candidates to show next to the input. */
	hints: string[] = $state([]);

	private cycle: Cycle | null = null;

	/** Opens the line; initial prefills it (the "+ save" tab starts with "save "). */
	start(initial = ''): void {
		this.text = initial;
		this.error = '';
		this.hints = [];
		this.cycle = null;
		history.reset();
		this.open = true;
	}

	cancel(): void {
		this.open = false;
		this.error = '';
		this.hints = [];
	}

	/** Called when the user types: completion and history start over. */
	typed(): void {
		this.error = '';
		this.hints = [];
		this.cycle = null;
		history.reset();
	}

	/**
	 * Parses the typed command. On success the line closes and the command is
	 * returned for the caller to run; on an error the line stays open and
	 * shows it.
	 */
	run(ctx: CommandContext): Command | null {
		const r = parseCommand(this.text, ctx);
		if (!r.ok) {
			this.error = r.error;
			return null;
		}
		history.push(this.text);
		this.open = false;
		this.hints = [];
		return r.cmd;
	}

	/**
	 * Tab: completes the word at the end; with several candidates it first
	 * completes their common prefix, then steps through them.
	 */
	complete(ctx: CommandContext): void {
		this.error = '';
		const cy = this.cycle;
		if (cy && this.text === cy.text) {
			cy.index = (cy.index + 1) % cy.candidates.length;
			this.set(cy.from, cy.candidates[cy.index]!, cy);
			return;
		}
		const { from, candidates } = completeCommand(this.text, ctx);
		this.cycle = null;
		this.hints = candidates.length > 1 ? candidates : [];
		const only = candidates[0];
		if (only === undefined) return;
		if (candidates.length === 1) {
			// A command that takes an argument gets the space to type it after.
			const takesArg = from === 0 && COMMANDS.some((c) => c.name === only && c.args);
			this.text = this.text.slice(0, from) + only + (takesArg ? ' ' : '');
			return;
		}
		const typed = this.text.length - from;
		const prefix = commonPrefix(candidates);
		if (prefix.length > typed) {
			this.text = this.text.slice(0, from) + prefix;
			return;
		}
		const next: Cycle = { from, candidates, index: 0, text: '' };
		this.cycle = next;
		this.set(from, only, next);
	}

	private set(from: number, word: string, cy: Cycle): void {
		this.text = this.text.slice(0, from) + word;
		cy.text = this.text;
	}

	historyPrev(): void {
		const t = history.prev(this.text);
		if (t !== null) this.recall(t);
	}

	historyNext(): void {
		const t = history.next();
		if (t !== null) this.recall(t);
	}

	private recall(t: string): void {
		this.text = t;
		this.error = '';
		this.hints = [];
		this.cycle = null;
	}
}
