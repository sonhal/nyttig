<!--
	One feed row. Desktop: a single 20px line, columns in the TUI's order
	(unviewed dot, date, source, tags, title, description, domain). Mobile:
	two lines in 44px, title on the second line, no description.

	All feed text is rendered as text; colors are validated before use.
-->
<script lang="ts">
	import { formatDate, formatTime } from './format';
	import type { SourceDisplay, TagDisplay } from './meta';
	import { domainOf, htmlToText, oneLine, safeColor } from './sanitize';
	import type { Item } from './types';

	interface Props {
		item: Item;
		index: number;
		selected: boolean;
		expanded: boolean;
		source: SourceDisplay | undefined;
		/** Current tags by ID; they win over the item's copy of its tags. */
		tags: Map<string, TagDisplay>;
		onselect: (index: number) => void;
	}

	let { item, index, selected, expanded, source, tags, onselect }: Props = $props();

	const title = $derived(oneLine(item.title) || '(untitled)');
	const desc = $derived(htmlToText(item.description));
	const domain = $derived(domainOf(item.link));
</script>

<!-- Clicks are a convenience; the grid is driven from the keyboard. -->
<!-- svelte-ignore a11y_click_events_have_key_events -->
<div
	class="row"
	class:selected
	class:viewed={item.viewed}
	class:nodesc={!desc}
	role="row"
	tabindex={-1}
	id="row-{item.id}"
	aria-selected={selected}
	aria-expanded={expanded}
	data-id={item.id}
	onclick={() => onselect(index)}
>
	<div class="cells" role="gridcell">
		<span class="dot" aria-label={item.viewed ? undefined : 'unviewed'}>{item.viewed ? '' : '●'}</span>
		<span class="date full">{formatDate(item.published)}</span>
		<span class="date short">{formatTime(item.published)}</span>
		{#if source?.label}
			<span class="chip src"
				>[<span style:color={safeColor(source.color)}>{oneLine(source.label)}</span>]</span
			>
		{/if}
		{#if item.tags?.length}
			<span class="tags">
				{#each item.tags as tag, i (tag.id ?? i)}
					{@const cur = tag.id ? tags.get(tag.id) : undefined}
					<span class="chip"
						>[<span style:color={cur ? cur.color : safeColor(tag.color)}>{oneLine(cur?.name || tag.name)}</span
						>]</span
					>
				{/each}
			</span>
		{/if}
		<span class="title">{title}</span>
		{#if desc}<span class="desc">{desc}</span>{/if}
		<span class="domain">{domain}</span>
	</div>
</div>

<style>
	.row {
		height: var(--row-h);
		overflow: hidden;
		cursor: default;
		user-select: none;
	}
	.row.selected {
		background: var(--sel);
	}
	@media (hover: hover) {
		.row:not(.selected):hover {
			background: #262628;
		}
	}
	.cells {
		display: flex;
		align-items: center;
		gap: 1ch;
		height: 100%;
		padding: 0 1ch;
		white-space: nowrap;
	}
	.dot {
		flex: none;
		width: 1ch;
		color: var(--unviewed);
		font-weight: bold;
	}
	.date {
		flex: none;
		color: var(--dim);
	}
	.date.full {
		width: 11ch;
	}
	.date.short {
		display: none;
	}
	.chip {
		color: var(--dim);
	}
	.src {
		flex: none;
	}
	.tags {
		flex: 0 1 auto;
		display: flex;
		gap: 1ch;
		min-width: 0;
		overflow: hidden;
	}
	.title {
		flex: 0 1 auto;
		max-width: 60%;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.nodesc .title {
		max-width: none;
	}
	.desc {
		flex: 1 1 0;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		color: var(--desc);
	}
	.domain {
		flex: none;
		margin-left: auto;
		color: var(--accent);
	}

	@media (max-width: 719.98px) {
		.cells {
			flex-wrap: wrap;
			align-content: center;
			row-gap: 0;
			column-gap: 1ch;
		}
		.date.full,
		.desc {
			display: none;
		}
		.date.short {
			display: inline;
		}
		.title {
			order: 10;
			flex: 1 0 100%;
			max-width: 100%;
			padding-left: 2ch;
		}
		.row.viewed .title {
			color: #b8b8b8;
		}
	}
</style>
