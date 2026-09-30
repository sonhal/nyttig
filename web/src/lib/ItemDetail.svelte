<!--
	The expanded row: the item as a key=value record, like a log line in
	Kibana, then the description as plain text and an explicit link.
-->
<script lang="ts">
	import Highlight from './Highlight.svelte';
	import type { SourceDisplay } from './meta';
	import { htmlToText, oneLine, safeLink } from './sanitize';
	import type { Item } from './types';

	interface Props {
		item: Item;
		source: SourceDisplay | undefined;
		terms?: readonly string[];
	}

	let { item, source, terms = [] }: Props = $props();

	const link = $derived(safeLink(item.link));
	const desc = $derived(htmlToText(item.description));
	const tags = $derived((item.tags ?? []).map((t) => oneLine(t.name)).join(','));

	function iso(ts: string | undefined): string {
		if (!ts) return '-';
		const d = new Date(ts);
		return Number.isNaN(d.getTime()) ? '-' : d.toISOString().replace(/:\d\d\.\d+Z$/, 'Z');
	}
	function q(s: string | undefined): string {
		const v = oneLine(s);
		return v ? JSON.stringify(v) : '-';
	}
</script>

<div class="detail" role="region" aria-label="item details">
	<div class="kv">
		<span><span class="k">id=</span>{item.id}</span>
		<span><span class="k">source=</span>{q(item.source_name || source?.label)}</span>
		<span><span class="k">published=</span>{iso(item.published)}</span>
		<span><span class="k">fetched=</span>{iso(item.fetched_at)}</span>
		<span><span class="k">author=</span>{q(item.author)}</span>
		<span><span class="k">tags=</span>{tags || '-'}</span>
	</div>
	{#if link}
		<div class="kv">
			<a class="open" href={link} target="_blank" rel="noopener noreferrer" referrerpolicy="no-referrer"
				>open ↗</a
			>
			<span class="link"><span class="k">link=</span>{link}</span>
		</div>
	{/if}
	{#if desc}<p class="desc"><Highlight text={desc} {terms} /></p>{/if}
</div>

<style>
	.detail {
		padding: 2px 1ch 4px 3ch;
		background: #252526;
		border-left: 2px solid var(--sel);
		min-height: 100%;
	}
	.kv {
		display: flex;
		flex-wrap: wrap;
		column-gap: 1.5ch;
	}
	.k {
		color: var(--dim);
	}
	.link {
		overflow-wrap: anywhere;
	}
	.open {
		white-space: nowrap;
	}
	.desc {
		margin: 4px 0 0;
		color: var(--desc);
		overflow-wrap: anywhere;
	}
	@media (max-width: 719.98px) {
		.detail {
			padding-left: 1ch;
		}
		.open {
			display: inline-block;
			padding: 8px 0;
			font-size: 16px;
		}
	}
</style>
