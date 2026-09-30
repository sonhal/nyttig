// State of the ":" command line, shared by the feed and the management
// views, and navigation between views.

import { goto } from '$app/navigation';
import { parseCommand, VIEW_PATHS, type View } from './command';
import { metadata } from './metadata.svelte';

/** The URL of a view; the feed keeps the filter it had last. */
export function viewHref(v: View): string {
	return v === 'feed' ? '/' + metadata.feedSearch : VIEW_PATHS[v];
}

export function goView(v: View): Promise<void> {
	return goto(viewHref(v));
}

export class CommandLine {
	open = $state(false);
	text = $state('');
	error = $state('');

	start(): void {
		this.text = '';
		this.error = '';
		this.open = true;
	}

	cancel(): void {
		this.open = false;
		this.error = '';
	}

	/** Runs the typed command. On an error the line stays open and shows it. */
	run(): void {
		const r = parseCommand(this.text);
		if (!r.ok) {
			this.error = r.error;
			return;
		}
		this.open = false;
		void goView(r.view);
	}
}
