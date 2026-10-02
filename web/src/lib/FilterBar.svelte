<!--
	The filter bar. Desktop: "/ query  src:[..] tag:[..] since:[..] sort:[..] unviewed:[..]"
	like the TUI, each chip clickable. Mobile: the query input plus buttons
	for the filter sheet and refresh.
-->
<script lang="ts">
	import type { Candidate } from './query';
	import { oneLine, safeColor } from './sanitize';
	import type { Filter, Source, Tag } from './types';

	interface Props {
		filter: Filter;
		sources: Source[];
		tags: Tag[];
		/** The query input's text, which is applied on Enter. */
		draft: string;
		input?: HTMLInputElement;
		/** Name suggestions for the operator under the caret, best first. */
		suggestions?: Candidate[];
		/** The highlighted suggestion, or -1. */
		active?: number;
		/** Why the query cannot be applied, shown under the bar. */
		error?: string;
		/** The caret moved or the text changed (completion works at the caret). */
		oncaret?: (pos: number) => void;
		onpick?: (c: Candidate) => void;
		onhelp?: () => void;
		onsearchfocus: () => void;
		onsearchblur: () => void;
		oncyclesource: () => void;
		oncycletag: () => void;
		oncyclesince: () => void;
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
		suggestions = [],
		active = -1,
		error = '',
		oncaret,
		onpick,
		onhelp,
		onsearchfocus,
		onsearchblur,
		oncyclesource,
		oncycletag,
		oncyclesince,
		ontogglesort,
		ontoggleunviewed,
		onopensheet,
		onrefresh
	}: Props = $props();

	const srcName = $derived(
		filter.source ? oneLine(sources.find((s) => s.id === filter.source)?.name) || '#' + filter.source : 'all'
	);
	function caret() {
		if (input) oncaret?.(input.selectionStart ?? input.value.length);
	}

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
			placeholder="search…  tag: src: is:unviewed since:"
			aria-invalid={error ? 'true' : undefined}
			aria-describedby={error ? 'query-error' : undefined}
			aria-controls="query-suggest"
			autocomplete="off"
			autocapitalize="off"
			spellcheck="false"
			enterkeyhint="search"
			maxlength="500"
			data-testid="search"
			oninput={caret}
			onkeyup={caret}
			onclick={caret}
			onfocus={() => {
				onsearchfocus();
				caret();
			}}
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
		<button type="button" onclick={oncyclesince} title="cycle time window (since:)" data-testid="chip-since"
			><span class="k">since:</span><span class="v">{filter.since || 'any'}</span></button
		>
		<button type="button" onclick={ontogglesort} title="toggle sort (o)"
			><span class="k">sort:</span><span class="v">{filter.sort}</span></button
		>
		<button type="button" onclick={ontoggleunviewed} title="unviewed only"
			><span class="k">unviewed:</span><span class="v">{filter.unviewed ? 'on' : 'off'}</span></button
		>
		<a class="count" href="/sources" title="manage sources (:sources)">[{sources.length} src]</a>
	</div>
	<div class="mobile-actions">
		<button type="button" class="icon" onclick={onopensheet} aria-label="filters" data-testid="open-filters"
			>⚙</button
		>
		<button type="button" class="icon" onclick={onrefresh} aria-label="refresh all sources">⟳</button>
		<button type="button" class="icon" onclick={onhelp} aria-label="help" data-testid="open-help">?</button>
	</div>
	{#if suggestions.length || error}
		<div class="drop" data-testid="query-drop">
			{#if suggestions.length}
				<ul id="query-suggest" role="listbox" aria-label="suggestions">
					{#each suggestions as c, i (c.insert)}
						<!-- Tab and the arrows drive it; a pointer must not take focus from the input. -->
						<!-- svelte-ignore a11y_click_events_have_key_events -->
						<li
							role="option"
							aria-selected={i === active}
							class:active={i === active}
							onpointerdown={(e) => e.preventDefault()}
							onclick={() => onpick?.(c)}
						>
							<span class="kind">{c.kind}:</span><span class="name" style:color={safeColor(c.color)}>{c.label}</span>
						</li>
					{/each}
				</ul>
			{/if}
			{#if error}<div class="qerr" id="query-error" role="alert" data-testid="query-error">{error}</div>{/if}
		</div>
	{/if}
</div>

<style>
	.bar {
		position: relative;
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
		text-decoration: none;
	}
	.mobile-actions {
		display: none;
	}
	.drop {
		position: absolute;
		z-index: 15;
		top: 100%;
		left: 0;
		right: 0;
		background: var(--bar);
		border-bottom: 1px solid var(--sel);
		white-space: normal;
	}
	.drop ul {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.drop li {
		height: 20px;
		padding: 0 1ch 0 calc(1ch + 1ch);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		cursor: default;
	}
	.drop li.active {
		background: var(--sel);
	}
	.kind {
		color: var(--dim);
	}
	.name {
		color: var(--fg);
	}
	.qerr {
		padding: 0 1ch;
		color: var(--error);
		overflow-wrap: anywhere;
	}

	@media (max-width: 719.98px) {
		.bar {
			gap: 1ch;
			padding: 0 8px;
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
		.drop li {
			height: 44px;
			line-height: 44px;
			font-size: 16px;
		}
		.qerr {
			padding: 6px 8px;
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
