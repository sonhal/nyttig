<!--
	Tags: add, rename or recolor (UpdateTag, which keeps rules and item
	assignments), and delete (which takes the tag's rules and item
	assignments with it). Tags are listed as a tree: a tag with several
	parents shows under each, the repeats muted.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import * as api from '$lib/api';
	import ConfirmPanel from '$lib/ConfirmPanel.svelte';
	import { defaultFilter } from '$lib/filter';
	import { tagAddBody, tagForm, tagPatchBody, type TagForm } from '$lib/forms';
	import type { ManageAction, Tool } from '$lib/keymap';
	import ManageView from '$lib/ManageView.svelte';
	import { metadata } from '$lib/metadata.svelte';
	import { oneLine, safeColor } from '$lib/sanitize';
	import TagFormPanel from '$lib/TagForm.svelte';
	import { buildTree, flatten, orphansOnDelete } from '$lib/tagtree';
	import type { Tag, TagRule } from '$lib/types';
	import { usageWarning, viewsUsing } from '$lib/views';

	const tools: Tool[] = [
		{ type: 'add', label: 'add', key: 'a' },
		{ type: 'edit', label: 'edit', key: 'e' },
		{ type: 'delete', label: 'delete', key: 'x' }
	];

	type Panel =
		| { kind: 'add' }
		| { kind: 'edit'; tag: Tag }
		| { kind: 'delete'; tag: Tag; items: number | null; error: string; busy: boolean };

	let cursor = $state(0);
	// Raw: panels are replaced whole, and async steps compare them by identity.
	let panel: Panel | null = $state.raw(null);
	let loaded = $state(false);
	let rules: TagRule[] = $state.raw([]);
	let note = $state('');
	let noteTimer: ReturnType<typeof setTimeout> | undefined;

	const tags = $derived(metadata.tags);
	const rows = $derived(flatten(buildTree(tags), { repeats: true }));
	const selected = $derived(rows[cursor]?.tag);
	const ruleCounts = $derived.by(() => {
		const m = new Map<string, number>();
		for (const r of rules) if (r.tag_id) m.set(r.tag_id, (m.get(r.tag_id) ?? 0) + 1);
		return m;
	});

	function flash(msg: string) {
		note = msg;
		clearTimeout(noteTimer);
		noteTimer = setTimeout(() => (note = ''), 5000);
	}

	function errMsg(e: unknown): string {
		return e instanceof Error ? e.message : String(e);
	}

	async function reload() {
		const [r] = await Promise.allSettled([api.listRules(), metadata.reload()]);
		if (r.status === 'fulfilled') rules = r.value;
		loaded = true;
	}

	function selectId(id: string | undefined) {
		const i = rows.findIndex((r) => r.tag.id === id && !r.repeat);
		if (i >= 0) cursor = i;
	}

	async function save(f: TagForm) {
		if (panel?.kind === 'edit') {
			const orig = panel.tag;
			const patch = tagPatchBody(orig, f);
			if (Object.keys(patch).length > 0) {
				await api.updateTag(orig.id ?? '', patch);
				flash(`saved ${oneLine(f.name)}`);
			}
			panel = null;
			await reload();
			selectId(orig.id);
		} else {
			const created = await api.addTag(tagAddBody(f));
			panel = null;
			flash(`added ${oneLine(created.name)}; add rules for it with :rules`);
			await reload();
			selectId(created.id);
		}
	}

	async function askDelete(t: Tag) {
		const p: Panel = { kind: 'delete', tag: t, items: null, error: '', busy: false };
		panel = p;
		try {
			// Exact: the count is the assignments this delete removes, not the
			// items the tag shows through its children.
			const page = await api.searchItems({ ...defaultFilter, tag: t.id ?? '' }, 1, 0, true);
			if (panel === p) panel = { ...p, items: page.total };
		} catch {
			// The confirmation says the count is unavailable.
		}
	}

	async function confirmDelete() {
		if (panel?.kind !== 'delete' || panel.busy) return;
		const p = panel;
		panel = { ...p, busy: true, error: '' };
		try {
			await api.removeTag(p.tag.id ?? '');
			panel = null;
			flash(`deleted ${oneLine(p.tag.name)}`);
			await reload();
		} catch (e) {
			if (api.isNotFound(e)) {
				// Another client removed it first: the goal is met, and the list
				// is stale, so refresh it instead of leaving the error up.
				panel = null;
				flash(`already deleted ${oneLine(p.tag.name)}`);
				await reload();
				return;
			}
			panel = { ...p, busy: false, error: errMsg(e) };
		}
	}

	function deleteLines(p: Extract<Panel, { kind: 'delete' }>): string[] {
		const n = ruleCounts.get(p.tag.id ?? '') ?? 0;
		const items = p.items === null ? 'every item that has it' : `${p.items} ${p.items === 1 ? 'item' : 'items'}`;
		const lines = [
			`This also deletes its ${n} ${n === 1 ? 'rule' : 'rules'} and removes it from ${items}.`
		];
		const orphans = orphansOnDelete(tags, p.tag.id ?? '').length;
		if (orphans > 0) {
			lines.push(`${orphans} child ${orphans === 1 ? 'tag becomes' : 'tags become'} top-level.`);
		}
		const w = usageWarning('tag', viewsUsing({ tag: p.tag.id ?? '' }, metadata.views).length);
		if (w) lines.push(w);
		lines.push('Use edit (e) instead to rename or recolor it.');
		return lines;
	}

	function onaction(a: ManageAction) {
		switch (a.type) {
			case 'add':
				panel = { kind: 'add' };
				return;
			case 'edit':
				if (selected) panel = { kind: 'edit', tag: selected };
				return;
			case 'delete':
				if (selected) void askDelete(selected);
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

<svelte:head><title>tags · nyttig</title></svelte:head>

<ManageView
	page="tags"
	items={rows}
	key={(r) => r.key}
	bind:cursor
	panel={panel === null ? null : panel.kind === 'delete' ? 'confirm' : 'form'}
	{tools}
	note={note || (metadata.error ? 'error: ' + metadata.error : '')}
	counts={{ sources: metadata.sources.length, tags: tags.length, rules: rules.length, views: metadata.views.length }}
	{loaded}
	{onaction}
>
	{#snippet row(r)}
		<span class="chip" class:repeat={r.repeat} style:padding-left="{r.depth * 2}ch"
			>[<span style:color={safeColor(r.tag.color)}>{oneLine(r.tag.name)}</span>]</span
		>
		<span class="color">{safeColor(r.tag.color) ?? 'no color'}</span>
		<span class="rules">{ruleCounts.get(r.tag.id ?? '') ?? 0} rules</span>
		{#if r.repeat}<span class="also">also under another parent</span>{/if}
	{/snippet}
	{#snippet panelContent()}
		{#if panel?.kind === 'add'}
			<TagFormPanel initial={tagForm()} editing={false} {tags} onsave={save} oncancel={() => (panel = null)} />
		{:else if panel?.kind === 'edit'}
			{#key panel.tag.id}
				<TagFormPanel
					initial={tagForm(panel.tag)}
					editing={true}
					{tags}
					selfId={panel.tag.id}
					onsave={save}
					oncancel={() => (panel = null)}
				/>
			{/key}
		{:else if panel?.kind === 'delete'}
			<ConfirmPanel
				title={`delete tag ${oneLine(panel.tag.name)}?`}
				lines={deleteLines(panel)}
				busy={panel.busy}
				error={panel.error}
				onconfirm={() => void confirmDelete()}
				oncancel={() => (panel = null)}
			/>
		{/if}
	{/snippet}
	{#snippet empty()}
		no tags yet — press <b>a</b> to add one
	{/snippet}
</ManageView>

<style>
	.chip {
		flex: 0 1 30ch;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		color: var(--dim);
	}
	.chip.repeat {
		opacity: 0.55;
	}
	.also {
		flex: none;
		color: var(--dim);
		opacity: 0.7;
	}
	.color {
		flex: none;
		width: 9ch;
		color: var(--dim);
	}
	.rules {
		flex: none;
		color: var(--dim);
	}
	b {
		color: var(--fg-strong);
		font-weight: normal;
	}
</style>
