<!-- Mobile filter sheet: native selects for source, tag, sort and unviewed. -->
<script lang="ts">
	import { onMount } from 'svelte';
	import type { TimeMode } from './command';
	import { oneLine } from './sanitize';
	import { SINCE_PRESETS } from './since';
	import { treeOrder } from './tagtree';
	import { withAssessor } from './filter';
	import type { Assessor, Filter, Source, Tag } from './types';

	interface Props {
		filter: Filter;
		sources: Source[];
		tags: Tag[];
		assessors?: Assessor[];
		timeMode: TimeMode;
		onchange: (f: Filter) => void;
		ontime: (mode: TimeMode) => void;
		onclose: () => void;
	}

	let { filter, sources, tags, assessors = [], timeMode, onchange, ontime, onclose }: Props = $props();

	// Tree order, children indented (non-breaking spaces survive in <option>).
	const tagRows = $derived(treeOrder(tags));
	// A window typed in the query bar (2w) is not one of the presets: list it too.
	const sinceOptions = $derived(SINCE_PRESETS.includes(filter.since) ? SINCE_PRESETS : [...SINCE_PRESETS, filter.since]);

	let first: HTMLSelectElement | undefined = $state();
	onMount(() => first?.focus());

	function set<K extends keyof Filter>(k: K, v: Filter[K]) {
		onchange({ ...filter, [k]: v });
	}
</script>

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="scrim" onclick={onclose}></div>
<div class="sheet" role="dialog" aria-modal="true" aria-label="Filters" data-testid="filter-sheet">
	<label>
		<span>source</span>
		<select bind:this={first} value={filter.source} onchange={(e) => set('source', e.currentTarget.value)}>
			<option value="">all</option>
			{#each sources as s (s.id)}
				<option value={s.id}>{oneLine(s.name)}</option>
			{/each}
		</select>
	</label>
	<label>
		<span>tag</span>
		<select value={filter.tag} onchange={(e) => set('tag', e.currentTarget.value)}>
			<option value="">all</option>
			{#each tagRows as r (r.key)}
				<option value={r.tag.id}>{'\u00a0\u00a0'.repeat(r.depth)}{oneLine(r.tag.name)}</option>
			{/each}
		</select>
	</label>
	{#if assessors.length > 0}
		<label>
			<span>score</span>
			<select value={filter.assessor} onchange={(e) => onchange(withAssessor(filter, e.currentTarget.value))}>
				<option value="">none</option>
				{#each assessors as a (a.id)}
					<option value={a.id}>{oneLine(a.name)}</option>
				{/each}
			</select>
		</label>
		<label>
			<span>unassessed</span>
			<select value={filter.unassessed} onchange={(e) => set('unassessed', e.currentTarget.value)}>
				<option value="">any</option>
				{#each assessors as a (a.id)}
					<option value={a.id}>{oneLine(a.name)}</option>
				{/each}
			</select>
		</label>
	{/if}
	<label>
		<span>since</span>
		<select value={filter.since} onchange={(e) => set('since', e.currentTarget.value)} data-testid="sheet-since">
			{#each sinceOptions as w (w)}
				<option value={w}>{w === '' ? 'any time' : 'last ' + w}</option>
			{/each}
		</select>
	</label>
	<label>
		<span>sort</span>
		<select
			value={filter.sort}
			onchange={(e) => {
				const v = e.currentTarget.value;
				set('sort', v === 'oldest' ? 'oldest' : v === 'score' && filter.assessor ? 'score' : 'newest');
			}}
		>
			<option value="newest">newest</option>
			<option value="oldest">oldest</option>
			{#if filter.assessor}<option value="score">score</option>{/if}
		</select>
	</label>
	<label>
		<span>unviewed</span>
		<select value={filter.unviewed ? '1' : ''} onchange={(e) => set('unviewed', e.currentTarget.value === '1')}>
			<option value="">all items</option>
			<option value="1">unviewed only</option>
		</select>
	</label>
	<label>
		<span>times</span>
		<select value={timeMode} onchange={(e) => ontime(e.currentTarget.value === 'relative' ? 'relative' : 'absolute')}>
			<option value="absolute">absolute (10:32)</option>
			<option value="relative">relative (12m)</option>
		</select>
	</label>
	<nav class="manage" aria-label="manage">
		<span>manage</span>
		<a href="/sources" data-testid="manage-sources">sources</a>
		<a href="/tags" data-testid="manage-tags">tags</a>
		<a href="/rules" data-testid="manage-rules">rules</a>
		<a href="/views" data-testid="manage-views">views</a>
		<a href="/assessors" data-testid="manage-assessors">assess</a>
		<a href="/digests" data-testid="manage-digests">digests</a>
	</nav>
	<button type="button" class="done" onclick={onclose}>done</button>
</div>

<style>
	.scrim {
		position: fixed;
		inset: 0;
		background: rgb(0 0 0 / 50%);
		z-index: 10;
	}
	.sheet {
		position: fixed;
		left: 0;
		right: 0;
		bottom: 0;
		z-index: 11;
		display: grid;
		gap: 12px;
		padding: 16px 16px calc(16px + env(safe-area-inset-bottom));
		background: var(--bar);
		border-top: 1px solid var(--sel);
		border-radius: 12px 12px 0 0;
	}
	label {
		display: grid;
		grid-template-columns: 10ch 1fr;
		align-items: center;
	}
	span {
		color: var(--dim);
	}
	select,
	.done {
		font-size: 16px;
		height: 44px;
		background: var(--bg);
		border: 1px solid var(--sel);
		border-radius: 6px;
		padding: 0 8px;
	}
	.done {
		color: var(--accent);
	}
	.manage {
		display: grid;
		grid-template-columns: repeat(3, 1fr);
		align-items: center;
		gap: 8px;
	}
	.manage span {
		grid-column: 1 / -1;
	}
	.manage a {
		display: grid;
		place-items: center;
		height: 44px;
		border: 1px solid var(--sel);
		border-radius: 6px;
		text-decoration: none;
	}
</style>
