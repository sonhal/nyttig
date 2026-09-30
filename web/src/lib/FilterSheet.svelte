<!-- Mobile filter sheet: native selects for source, tag, sort and unviewed. -->
<script lang="ts">
	import { onMount } from 'svelte';
	import { oneLine } from './sanitize';
	import type { Filter, Source, Tag } from './types';

	interface Props {
		filter: Filter;
		sources: Source[];
		tags: Tag[];
		onchange: (f: Filter) => void;
		onclose: () => void;
	}

	let { filter, sources, tags, onchange, onclose }: Props = $props();

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
			{#each tags as t (t.id)}
				<option value={t.id}>{oneLine(t.name)}</option>
			{/each}
		</select>
	</label>
	<label>
		<span>sort</span>
		<select
			value={filter.sort}
			onchange={(e) => set('sort', e.currentTarget.value === 'oldest' ? 'oldest' : 'newest')}
		>
			<option value="newest">newest</option>
			<option value="oldest">oldest</option>
		</select>
	</label>
	<label>
		<span>unviewed</span>
		<select value={filter.unviewed ? '1' : ''} onchange={(e) => set('unviewed', e.currentTarget.value === '1')}>
			<option value="">all items</option>
			<option value="1">unviewed only</option>
		</select>
	</label>
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
</style>
