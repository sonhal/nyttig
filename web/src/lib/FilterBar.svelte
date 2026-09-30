<!--
	The filter bar. Desktop: "/ query  src:[..] tag:[..] sort:[..] unviewed:[..]"
	like the TUI, each chip clickable. Mobile: the query input plus buttons
	for the filter sheet and refresh.
-->
<script lang="ts">
	import type { Filter, Source, Tag } from './types';
	import { oneLine } from './sanitize';

	interface Props {
		filter: Filter;
		sources: Source[];
		tags: Tag[];
		/** The query input's text, which is applied on Enter. */
		draft: string;
		input?: HTMLInputElement;
		onsearchfocus: () => void;
		onsearchblur: () => void;
		oncyclesource: () => void;
		oncycletag: () => void;
		ontogglesort: () => void;
		ontoggleunviewed: () => void;
		onopensheet: () => void;
		onrefresh: () => void;
	}

	let {
		filter,
		sources,
		tags,
		draft = $bindable(),
		input = $bindable(),
		onsearchfocus,
		onsearchblur,
		oncyclesource,
		oncycletag,
		ontogglesort,
		ontoggleunviewed,
		onopensheet,
		onrefresh
	}: Props = $props();

	const srcName = $derived(
		filter.source ? oneLine(sources.find((s) => s.id === filter.source)?.name) || '#' + filter.source : 'all'
	);
	const tagName = $derived(
		filter.tag ? oneLine(tags.find((t) => t.id === filter.tag)?.name) || '#' + filter.tag : 'all'
	);
</script>

<div class="bar" role="search">
	<label class="query">
		<span class="slash" aria-hidden="true">/</span>
		<span class="visually-hidden">Search</span>
		<input
			bind:this={input}
			bind:value={draft}
			type="search"
			placeholder="search…"
			autocomplete="off"
			autocapitalize="off"
			spellcheck="false"
			enterkeyhint="search"
			maxlength="500"
			data-testid="search"
			onfocus={onsearchfocus}
			onblur={onsearchblur}
		/>
	</label>
	<div class="chips">
		<button type="button" onclick={oncyclesource} title="cycle source (s)"
			><span class="k">src:</span><span class="v">{srcName}</span></button
		>
		<button type="button" onclick={oncycletag} title="cycle tag (t)"
			><span class="k">tag:</span><span class="v">{tagName}</span></button
		>
		<button type="button" onclick={ontogglesort} title="toggle sort (o)"
			><span class="k">sort:</span><span class="v">{filter.sort}</span></button
		>
		<button type="button" onclick={ontoggleunviewed} title="unviewed only"
			><span class="k">unviewed:</span><span class="v">{filter.unviewed ? 'on' : 'off'}</span></button
		>
		<span class="count">[{sources.length} src]</span>
	</div>
	<div class="mobile-actions">
		<button type="button" class="icon" onclick={onopensheet} aria-label="filters" data-testid="open-filters"
			>⚙</button
		>
		<button type="button" class="icon" onclick={onrefresh} aria-label="refresh all sources">⟳</button>
	</div>
</div>

<style>
	.bar {
		display: flex;
		align-items: center;
		gap: 2ch;
		height: var(--bar-h);
		padding: 0 1ch;
		background: var(--bar-filter);
		white-space: nowrap;
	}
	.query {
		display: flex;
		align-items: center;
		flex: 0 1 36ch;
		min-width: 12ch;
	}
	.slash {
		color: var(--dim);
	}
	input {
		flex: 1;
		min-width: 0;
		background: transparent;
		border: none;
		outline: none;
		padding: 0 0 0 0.5ch;
		color: var(--fg-strong);
		caret-color: var(--unviewed);
	}
	input:focus {
		color: var(--unviewed);
		text-decoration: underline;
	}
	input::placeholder {
		color: #5a5a5a;
	}
	input::-webkit-search-cancel-button {
		display: none;
	}
	.chips {
		display: flex;
		gap: 2ch;
		flex: 1;
		min-width: 0;
		overflow: hidden;
	}
	.chips button {
		background: none;
		border: none;
		padding: 0;
		cursor: pointer;
	}
	.k {
		color: var(--dim);
	}
	.v {
		color: var(--fg-strong);
	}
	.v::before {
		content: '[';
		color: var(--dim);
	}
	.v::after {
		content: ']';
		color: var(--dim);
	}
	.count {
		margin-left: auto;
		color: var(--dim);
	}
	.mobile-actions {
		display: none;
	}

	@media (max-width: 719.98px) {
		.bar {
			gap: 1ch;
			padding: 0 8px;
			padding-top: env(safe-area-inset-top);
			height: calc(var(--bar-h) + env(safe-area-inset-top));
		}
		.chips {
			display: none;
		}
		.query {
			flex: 1;
			background: var(--bg);
			border: 1px solid var(--sel);
			border-radius: 4px;
			padding: 0 8px;
			height: 34px;
		}
		.slash {
			display: none;
		}
		/* 16px keeps iOS from zooming in on focus. */
		input {
			font-size: 16px;
			padding: 0;
		}
		input:focus {
			text-decoration: none;
		}
		.mobile-actions {
			display: flex;
			gap: 4px;
		}
		.icon {
			width: 40px;
			height: 40px;
			font-size: 20px;
			background: none;
			border: 1px solid var(--sel);
			border-radius: 4px;
		}
	}
</style>
