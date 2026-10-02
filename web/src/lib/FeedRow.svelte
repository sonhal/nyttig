<!--
	One feed row. Desktop: a single 20px line, columns in the TUI's order
	(unviewed dot, date, source, tags, title, description, domain). Mobile:
	one line in 44px (a touch target) with a short date and no description
	or domain, and the first tag with a count of the others; the tag name is
	cut with an ellipsis before the title gets narrower than 40% of the row. Viewed (read) rows are dimmed.

	All feed text is rendered as text; colors are validated before use.
-->
<script lang="ts">
	import type { TimeMode } from './command';
	import { formatDate, formatRelative, formatTime } from './format';
	import Highlight from './Highlight.svelte';
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
		/** The free-text words of the query, marked in the title and description. */
		terms?: readonly string[];
		timeMode?: TimeMode;
		/** The current time, for relative dates. */
		now?: number;
	}

	let {
		item,
		index,
		selected,
		expanded,
		source,
		tags,
		onselect,
		terms = [],
		timeMode = 'absolute',
		now = 0
	}: Props = $props();

	const title = $derived(oneLine(item.title) || '(untitled)');
	const desc = $derived(htmlToText(item.description));
	const domain = $derived(domainOf(item.link));
	const relative = $derived(timeMode === 'relative');
	const dateFull = $derived(relative ? formatRelative(item.published, now) : formatDate(item.published));
	const dateShort = $derived(relative ? formatRelative(item.published, now, true) : formatTime(item.published));
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
		<span class="date full">{dateFull}</span>
		<span class="date short">{dateShort}</span>
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
						>[<span class="name" style:color={cur ? cur.color : safeColor(tag.color)}>{oneLine(cur?.name || tag.name)}</span
						>]</span
					>
				{/each}
				{#if item.tags.length > 1}<span class="more">+{item.tags.length - 1}</span>{/if}
			</span>
		{/if}
		<span class="title"><Highlight text={title} {terms} /></span>
		{#if desc}<span class="desc"><Highlight text={desc} {terms} /></span>{/if}
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
	/* Read rows are dimmed as a whole, colored chips included; the selected
	   one less, so it stays comfortable to read. */
	.row.viewed .cells {
		opacity: 0.45;
	}
	.row.viewed.selected .cells {
		opacity: 0.75;
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
	.more {
		display: none;
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
		.date.full,
		.desc,
		.domain {
			display: none;
		}
		.date.short {
			display: inline;
		}
		/* One tag and a count of the others. A name that doesn't fit is cut
		   with an ellipsis inside its brackets, so a chip is never cut in
		   half. The expanded row lists them all. */
		.tags .chip:not(:first-child) {
			display: none;
		}
		.more {
			display: inline;
			flex: none;
			color: var(--dim);
		}
		.tags .chip {
			display: flex;
			min-width: 0;
		}
		/* At least a letter and the ellipsis, never "[]". */
		.tags .name {
			min-width: 2ch;
			overflow: hidden;
			text-overflow: ellipsis;
		}
		/* The title takes what the tags leave, and at least 40% of the row:
		   with a basis of 0 and that floor, the tags shrink first. */
		.title {
			flex: 1 1 0;
			min-width: 40%;
			max-width: none;
		}
	}
</style>
