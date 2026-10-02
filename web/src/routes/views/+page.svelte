<!--
	Saved views: add, edit, delete, favorite (the tabs above the feed) and
	reorder (J/K). A view's filter is shown as the "/" bar's query text. The
	list is every view in display order; the tabs are the favorites in that
	order.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import * as api from '$lib/api';
	import ConfirmPanel from '$lib/ConfirmPanel.svelte';
	import { viewAddBody, viewForm, viewPatchBody, type ViewForm } from '$lib/forms';
	import type { ManageAction, Tool } from '$lib/keymap';
	import ManageView from '$lib/ManageView.svelte';
	import { metadata } from '$lib/metadata.svelte';
	import { format } from '$lib/query';
	import { oneLine } from '$lib/sanitize';
	import type { SavedView } from '$lib/types';
	import ViewFormPanel from '$lib/ViewForm.svelte';
	import { moved, viewToFilter } from '$lib/views';

	const tools: Tool[] = [
		{ type: 'add', label: 'add', key: 'a' },
		{ type: 'edit', label: 'edit', key: 'e' },
		{ type: 'delete', label: 'delete', key: 'x' },
		{ type: 'toggle', label: 'fav', key: 'space' },
		{ type: 'moveUp', label: 'up', key: 'K' },
		{ type: 'moveDown', label: 'down', key: 'J' }
	];

	type Panel =
		| { kind: 'add' }
		| { kind: 'edit'; view: SavedView }
		| { kind: 'delete'; view: SavedView; error: string; busy: boolean };

	let cursor = $state(0);
	// Raw: panels are replaced whole, and async steps compare them by identity.
	let panel: Panel | null = $state.raw(null);
	let loaded = $state(false);
	let note = $state('');
	let noteTimer: ReturnType<typeof setTimeout> | undefined;

	const views = $derived(metadata.views);
	const selected = $derived(views[cursor]);

	function flash(msg: string) {
		note = msg;
		clearTimeout(noteTimer);
		noteTimer = setTimeout(() => (note = ''), 5000);
	}

	function errMsg(e: unknown): string {
		return e instanceof Error ? e.message : String(e);
	}

	async function reload() {
		await metadata.reload();
		loaded = true;
	}

	function selectId(id: string | undefined) {
		const i = views.findIndex((v) => v.id === id);
		if (i >= 0) cursor = i;
	}

	async function save(f: ViewForm) {
		const { sources, tags, assessors } = metadata;
		if (panel?.kind === 'edit') {
			const orig = panel.view;
			const patch = viewPatchBody(orig, f, sources, tags, assessors);
			if (Object.keys(patch).length > 0) {
				await api.updateView(orig.id ?? '', patch);
				flash(`saved ${oneLine(f.name)}`);
			}
			panel = null;
			await reload();
			selectId(orig.id);
		} else {
			const created = await api.addView(viewAddBody(f, sources, tags, assessors));
			panel = null;
			flash(`added ${oneLine(created.name)}`);
			await reload();
			selectId(created.id);
		}
	}

	async function toggle(v: SavedView) {
		try {
			const updated = await api.updateView(v.id ?? '', { favorite: !v.favorite });
			flash(`${updated.favorite ? 'favorited' : 'unfavorited'} ${oneLine(v.name)}`);
			await reload();
		} catch (e) {
			flash('error: ' + errMsg(e));
		}
	}

	/** J/K: one place down or up the list; the tabs follow the order. */
	async function move(v: SavedView, by: number) {
		const ids = views.map((x) => x.id ?? '');
		const from = ids.indexOf(v.id ?? '');
		const next = moved(ids, from, by);
		if (next.every((id, i) => id === ids[i])) return;
		try {
			metadata.views = await api.reorderViews(next);
			selectId(v.id);
		} catch (e) {
			flash('error: ' + errMsg(e));
			await reload();
		}
	}

	async function confirmDelete() {
		if (panel?.kind !== 'delete' || panel.busy) return;
		const p = panel;
		panel = { ...p, busy: true, error: '' };
		try {
			await api.removeView(p.view.id ?? '');
			panel = null;
			flash(`deleted ${oneLine(p.view.name)}`);
			await reload();
		} catch (e) {
			if (api.isNotFound(e)) {
				// Another client removed it first: the goal is met.
				panel = null;
				flash(`already deleted ${oneLine(p.view.name)}`);
				await reload();
				return;
			}
			panel = { ...p, busy: false, error: errMsg(e) };
		}
	}

	function onaction(a: ManageAction) {
		switch (a.type) {
			case 'add':
				panel = { kind: 'add' };
				return;
			case 'edit':
				if (selected) panel = { kind: 'edit', view: selected };
				return;
			case 'delete':
				if (selected) panel = { kind: 'delete', view: selected, error: '', busy: false };
				return;
			case 'toggle':
				if (selected) void toggle(selected);
				return;
			case 'moveUp':
				if (selected) void move(selected, -1);
				return;
			case 'moveDown':
				if (selected) void move(selected, 1);
				return;
			case 'confirm':
				return void confirmDelete();
			case 'cancel':
				panel = null;
				return;
		}
	}

	onMount(() => {
		void reload();
		return () => clearTimeout(noteTimer);
	});
</script>

<svelte:head><title>views · nyttig</title></svelte:head>

<ManageView
	page="views"
	items={views}
	key={(v) => v.id ?? ''}
	bind:cursor
	panel={panel === null ? null : panel.kind === 'delete' ? 'confirm' : 'form'}
	{tools}
	note={note || (metadata.error ? 'error: ' + metadata.error : '')}
	counts={{ sources: metadata.sources.length, tags: metadata.tags.length, views: views.length, assessors: metadata.assessors.length }}
	{loaded}
	{onaction}
>
	{#snippet row(v)}
		<span class="fav" class:off={!v.favorite} title={v.favorite ? 'favorite (a tab)' : 'not a favorite'}
			>{v.favorite ? '★' : '☆'}</span
		>
		<span class="name">{oneLine(v.name)}</span>
		<span class="query" data-testid="view-filter"
			>{format(viewToFilter(v), metadata.sources, metadata.tags, metadata.assessors) || 'no filter'}</span
		>
	{/snippet}
	{#snippet panelContent()}
		{#if panel?.kind === 'add'}
			<ViewFormPanel
				initial={viewForm()}
				editing={false}
				sources={metadata.sources}
				tags={metadata.tags}
				assessors={metadata.assessors}
				onsave={save}
				oncancel={() => (panel = null)}
			/>
		{:else if panel?.kind === 'edit'}
			{#key panel.view.id}
				<ViewFormPanel
					initial={viewForm(panel.view, metadata.sources, metadata.tags, metadata.assessors)}
					editing={true}
					sources={metadata.sources}
					tags={metadata.tags}
					assessors={metadata.assessors}
					onsave={save}
					oncancel={() => (panel = null)}
				/>
			{/key}
		{:else if panel?.kind === 'delete'}
			<ConfirmPanel
				title={`delete view ${oneLine(panel.view.name)}?`}
				lines={['The view and its tab go away. Sources, tags and items are not affected.']}
				busy={panel.busy}
				error={panel.error}
				onconfirm={() => void confirmDelete()}
				oncancel={() => (panel = null)}
			/>
		{/if}
	{/snippet}
	{#snippet empty()}
		no saved views yet — press <b>a</b> to add one, or <b>:save &lt;name&gt;</b> in the feed
	{/snippet}
</ManageView>

<style>
	.fav {
		flex: none;
		color: var(--unviewed);
	}
	.fav.off {
		color: var(--dim);
	}
	.name {
		flex: 0 1 24ch;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		color: var(--fg-strong);
	}
	.query {
		flex: 1 1 0;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		color: var(--dim);
	}
	b {
		color: var(--fg-strong);
		font-weight: normal;
	}
</style>
