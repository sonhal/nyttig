<!--
	The expanded row: the item as a key=value record, like a log line in
	Kibana, then the description as plain text and an explicit link.
-->
<script lang="ts">
	import Highlight from './Highlight.svelte';
	import { formatRelative } from './format';
	import type { AssessorDisplay, SourceDisplay, TagDisplay } from './meta';
	import { htmlToText, oneLine, safeLink } from './sanitize';
	import { formatScore } from './scores';
	import type { Item } from './types';

	interface Props {
		item: Item;
		source: SourceDisplay | undefined;
		terms?: readonly string[];
		/** Current assessors and tags by ID, for the names the assessments show. */
		assessors?: Map<string, AssessorDisplay>;
		tagNames?: Map<string, TagDisplay>;
		/** The current time, for the age of an assessment. */
		now?: number;
	}

	let { item, source, terms = [], assessors = new Map(), tagNames = new Map(), now = 0 }: Props = $props();

	const assessments = $derived(item.assessments ?? []);

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
	{#if assessments.length}
		<ul class="assessments" aria-label="assessments" data-testid="assessments">
			{#each assessments as a (a.id ?? `${a.assessor_id}/${a.tag_id}`)}
				{@const cur = a.assessor_id ? assessors.get(a.assessor_id) : undefined}
				<!-- The assessor's name and the note are untrusted: text only. -->
				<li data-testid="assessment">
					<span class="k">assessor=</span><span style:color={cur?.color}>{oneLine(cur?.name || a.assessor_name) || '-'}</span>
					<span class="k">tag=</span>{a.tag_id ? oneLine(tagNames.get(a.tag_id)?.name) || '#' + a.tag_id : '-'}
					<span class="k">score=</span>{typeof a.score === 'number' ? formatScore(a.score) : '-'}
					<span class="k">age=</span>{formatRelative(a.updated_at, now) || '-'}
					{#if a.note}<span class="note"><span class="k">note=</span>{htmlToText(a.note)}</span>{/if}
				</li>
			{/each}
		</ul>
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
	.assessments {
		margin: 4px 0 0;
		padding: 0;
		list-style: none;
	}
	.assessments li {
		overflow-wrap: anywhere;
	}
	.note {
		color: var(--desc);
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
