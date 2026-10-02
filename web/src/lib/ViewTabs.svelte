<!--
	The saved views as tabs, above the filter bar: "all", one tab per favorite
	view (the first nine with their number key) and "+ save". The active
	tab is marked like the management pages' current tab, and shows "*" when
	the feed's filter differs from what the view saved. Tabs are plain links
	(a bookmark or middle-click works); picking the active tab again resets it.

	Text only: view names are interpolated, never rendered as HTML. The row
	scrolls sideways inside itself, so a long list never scrolls the page.
-->
<script lang="ts">
	import { oneLine } from './sanitize';
	import type { SavedView } from './types';
	import { NUMBERED_TABS, viewHref } from './views';

	interface Props {
		/** The favorite views, in tab order. */
		tabs: SavedView[];
		/** The open view, which gets a tab even when it is not a favorite. */
		active?: SavedView;
		/** The filter was changed since the view was saved. */
		modified: boolean;
		/** A tab was used: the feed takes the keyboard back. */
		onnavigate: () => void;
		/** "+ save": asks for a name. */
		onsave: () => void;
	}

	let { tabs, active, modified, onnavigate, onsave }: Props = $props();

	const shown = $derived(active && !tabs.some((v) => v.id === active.id) ? [...tabs, active] : tabs);
</script>

<nav class="tabs" aria-label="saved views" data-testid="view-tabs">
	<a
		href="/"
		class:current={!active}
		aria-current={!active ? 'page' : undefined}
		data-testid="view-tab-all"
		onclick={onnavigate}>all</a
	>
	{#each shown as v, i (v.id)}
		{@const current = v.id === active?.id}
		<a
			href={viewHref(v)}
			class:current
			aria-current={current ? 'page' : undefined}
			data-testid="view-tab"
			data-view-id={v.id}
			onclick={onnavigate}
			>{#if i < NUMBERED_TABS && v.favorite}<span class="n">{i + 1}{' '}</span>{/if}{oneLine(v.name)}{#if current && modified}<span
					class="mod"
					title="the filter differs from the saved view (:save to update it)"
					data-testid="view-modified">*</span
				>{/if}</a
		>
	{/each}
	<button type="button" class="save" onclick={onsave} data-testid="view-save">+ save</button>
</nav>

<style>
	.tabs {
		display: flex;
		align-items: center;
		gap: 2ch;
		height: var(--bar-h);
		padding: 0 1ch;
		background: var(--bar);
		white-space: nowrap;
		overflow-x: auto;
		overflow-y: hidden;
		overscroll-behavior-x: contain;
		scrollbar-width: none;
	}
	.tabs::-webkit-scrollbar {
		display: none;
	}
	a,
	.save {
		flex: none;
		color: var(--dim);
		text-decoration: none;
	}
	a.current {
		color: var(--fg-strong);
	}
	a.current::before {
		content: '[';
		color: var(--dim);
	}
	a.current::after {
		content: ']';
		color: var(--dim);
	}
	.n {
		color: var(--dim);
	}
	.mod {
		color: var(--unviewed);
	}
	.save {
		background: none;
		border: none;
		padding: 0;
		cursor: pointer;
		margin-left: auto;
	}

	@media (max-width: 719.98px) {
		.tabs {
			gap: 0;
			padding: env(safe-area-inset-top) 0 0;
			height: calc(var(--bar-h) + env(safe-area-inset-top));
		}
		a,
		.save {
			display: flex;
			align-items: center;
			height: 100%;
			min-width: 44px;
			padding: 0 12px;
			justify-content: center;
		}
		.n {
			display: none;
		}
		a.current {
			background: var(--sel);
		}
		a.current::before,
		a.current::after {
			content: none;
		}
		.save {
			margin-left: 0;
		}
	}
</style>
