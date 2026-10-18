<!--
	One feed row. Desktop: a single 20px line, columns in the TUI's order
	(unviewed dot, date, source, tags, title, description, domain). Mobile:
	two lines in 44px, title on the second line, no description. The first
	line (.meta) never wraps, so a row can't grow a third line. Viewed
	(read) rows are dimmed.

	columns (columns.ts) picks which cells show, in this same order; [] shows
	them all. The dot always shows. On mobile, with the title hidden, the
	description (when shown) takes the second line instead.

	All feed text is rendered as text; colors are validated before use.
-->
<script lang="ts">
	import { shows, type Column } from './columns';
	import type { TimeMode } from './command';
	import { archiveBadge } from './archives';
	import { formatDate, formatRelative, formatTime } from './format';
	import Highlight from './Highlight.svelte';
	import type { AssessorDisplay, SourceDisplay, TagDisplay } from './meta';
	import { domainOf, htmlToText, oneLine, safeColor } from './sanitize';
	import { formatScore, scoreChips } from './scores';
	import type { Item } from './types';

	interface Props {
		item: Item;
		index: number;
		selected: boolean;
		expanded: boolean;
		source: SourceDisplay | undefined;
		/** Current tags by ID; they win over the item's copy of its tags. */
		tags: Map<string, TagDisplay>;
		/** Current assessors by ID, for the score chips' names and colors. */
		assessors?: Map<string, AssessorDisplay>;
		/** The assessor whose scores the feed uses: its chip comes first. */
		selectedAssessor?: string;
		/** Score heat, 0 to 1: how strongly the row glows (see heatOf). */
		heat?: number;
		/** The columns to show (columns.ts); [] = all. */
		columns?: Column[];
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
		assessors = new Map(),
		selectedAssessor = '',
		heat = 0,
		columns = [],
		onselect,
		terms = [],
		timeMode = 'absolute',
		now = 0
	}: Props = $props();

	const show = (c: Column) => shows(columns, c);
	const badge = $derived(archiveBadge(item.archive));
	const chips = $derived(scoreChips(item, selectedAssessor));
	const title = $derived(oneLine(item.title) || '(untitled)');
	const desc = $derived(htmlToText(item.description));
	const domain = $derived(domainOf(item.link));
	const relative = $derived(timeMode === 'relative');
	// Without a published date (the feed gave none, or one the daemon could
	// not read) the fetch time is shown instead, in italics.
	const fetchedOnly = $derived(!item.published && !!item.fetched_at);
	const ts = $derived(item.published || item.fetched_at);
	const dateTitle = $derived(fetchedOnly ? 'fetched; the feed gave no date' : undefined);
	const dateFull = $derived(relative ? formatRelative(ts, now) : formatDate(ts));
	const dateShort = $derived(relative ? formatRelative(ts, now, true) : formatTime(ts));
</script>

<!-- Clicks are a convenience; the grid is driven from the keyboard. -->
<!-- svelte-ignore a11y_click_events_have_key_events -->
<div
	class="row"
	class:selected
	class:viewed={item.viewed}
	class:hot={heat > 0}
	style:--heat={heat > 0 ? heat : undefined}
	class:nodesc={!desc || !show('desc')}
	class:notitle={!show('title')}
	role="row"
	tabindex={-1}
	id="row-{item.id}"
	aria-selected={selected}
	aria-expanded={expanded}
	data-id={item.id}
	onclick={() => onselect(index)}
>
	<div class="cells" role="gridcell">
		<span class="meta">
			<span class="dot" aria-label={item.viewed ? undefined : 'unviewed'}>{item.viewed ? '' : '●'}</span>
			{#if show('date')}
				<span class="date full" class:fetched={fetchedOnly} title={dateTitle}>{dateFull}</span>
				<span class="date short" class:fetched={fetchedOnly} title={dateTitle}>{dateShort}</span>
			{/if}
			{#if source?.label && show('source')}
				<span class="chip src"
					>[<span style:color={safeColor(source.color)}>{oneLine(source.label)}</span>]</span
				>
			{/if}
			{#if item.tags?.length && show('tags')}
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
			{#each show('score') ? chips : [] as c (c.assessorId)}
				{@const cur = assessors.get(c.assessorId)}
				<span class="chip score" class:selected-assessor={c.assessorId === selectedAssessor} data-testid="score-chip"
					>[<span style:color={cur?.color}>{oneLine(cur?.name || c.name)} {formatScore(c.score)}</span>]</span
				>
			{/each}
			{#if show('domain')}<span class="domain">{domain}</span>{/if}
			{#if badge}
				<span
					class="badge {item.archive?.status}"
					title={badge.label}
					role="img"
					aria-label={badge.label}
					data-testid="archive-badge"
					data-status={item.archive?.status}>{badge.glyph}</span
				>
			{/if}
		</span>
		{#if show('title')}<span class="title"><Highlight text={title} {terms} /></span>{/if}
		{#if desc && show('desc')}<span class="desc"><Highlight text={desc} {terms} /></span>{/if}
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
		background-color: var(--sel);
	}
	/* Score heat: a gradient image over the row's background color (selection
	   and hover show beneath it) and a bar on the left. Not dimmed with
	   viewed rows: only .cells is. */
	.row.hot {
		background-image: linear-gradient(90deg, rgb(var(--heat-rgb) / calc(0.08 + var(--heat) * 0.27)), transparent 75%);
		box-shadow: inset 3px 0 0 rgb(var(--heat-rgb) / calc(0.35 + var(--heat) * 0.65));
	}
	@media (hover: hover) {
		.row:not(.selected):hover {
			background-color: #262628;
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
	/* The first mobile line. On desktop its cells are plain columns of the row. */
	.meta {
		display: contents;
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
	.date.fetched {
		font-style: italic;
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
	.score {
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
	.badge {
		flex: none;
		order: 2;
		color: var(--accent);
	}
	.badge.pending,
	.badge.running,
	.badge.partial {
		color: var(--dim);
	}
	.badge.failed {
		color: var(--error);
	}
	.domain {
		flex: none;
		order: 1;
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
		/* Line 1 never wraps: a row is a fixed 44px, and a third line would be
		   clipped at the top and bottom. Tags and the domain shrink instead. */
		.meta {
			display: flex;
			flex: 1 0 100%;
			align-items: center;
			gap: 1ch;
			min-width: 0;
			overflow: hidden;
		}
		.domain {
			flex: 0 1 auto;
			min-width: 0;
			overflow: hidden;
			text-overflow: ellipsis;
		}
		.title {
			order: 10;
			flex: 1 0 100%;
			max-width: 100%;
			padding-left: 2ch;
		}
		/* No title column: the description, when shown, is the second line. */
		.notitle .desc {
			display: inline;
			order: 10;
			flex: 1 0 100%;
			padding-left: 2ch;
		}
	}
</style>
