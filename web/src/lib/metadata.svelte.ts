// Sources, tags, saved views and assessors, shared by every page. The feed uses them for source
// labels and tag colors; the management views edit them and reload them
// after every change, so the feed shows new names, colors and
// abbreviations as soon as it is back on screen.

import * as api from './api';
import type { Assessor, SavedView, Source, Tag } from './types';

class Metadata {
	sources: Source[] = $state.raw([]);
	tags: Tag[] = $state.raw([]);
	views: SavedView[] = $state.raw([]);
	assessors: Assessor[] = $state.raw([]);
	/** The last load error, or "" after a successful load. */
	error = $state('');
	/**
	 * The feed's last query string ("?tag=3" or "?view=2&tag=3", or ""), so
	 * ":feed" and the nav links return to the same filter and the same tab.
	 */
	feedSearch = $state('');

	async reloadSources(): Promise<void> {
		try {
			this.sources = await api.listSources();
			this.error = '';
		} catch (e) {
			this.error = e instanceof Error ? e.message : String(e);
		}
	}

	async reloadTags(): Promise<void> {
		try {
			this.tags = await api.listTags();
			this.error = '';
		} catch (e) {
			this.error = e instanceof Error ? e.message : String(e);
		}
	}

	async reloadViews(): Promise<void> {
		try {
			this.views = await api.listViews();
			this.error = '';
		} catch (e) {
			this.error = e instanceof Error ? e.message : String(e);
		}
	}

	async reloadAssessors(): Promise<void> {
		try {
			this.assessors = await api.listAssessors();
			this.error = '';
		} catch (e) {
			this.error = e instanceof Error ? e.message : String(e);
		}
	}

	async reload(): Promise<void> {
		await Promise.all([this.reloadSources(), this.reloadTags(), this.reloadViews(), this.reloadAssessors()]);
	}
}

export const metadata = new Metadata();
