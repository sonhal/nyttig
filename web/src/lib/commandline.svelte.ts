// State of the ":" command line, shared by the feed and the management
// views, running commands, and navigation between views.

import { goto } from '$app/navigation';
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
import { filterFromParams, filterQuery } from './filter';
import { History } from './history';
import { metadata } from './metadata.svelte';
import { prefs } from './prefs.svelte';
import type { Filter } from './types';

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
	follow(): void;
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
		case 'help':
			host.help();
			return;
		case 'follow':
			host.follow();
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
			const f = applyToFilter(cmd, host.filter());
			if (f) host.setFilter(f);
		}
	}
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
		setFilter: (f) => void goto('/' + filterQuery(f)),
		follow: () => void goPage('feed')
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

	start(): void {
		this.text = '';
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
