<!--
	Tags: add, rename or recolor (UpdateTag, which keeps rules and item
	assignments), and delete (which takes the tag's rules and item
	assignments with it).
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
	import type { Tag, TagRule } from '$lib/types';

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
	const selected = $derived(tags[cursor]);
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
		const i = tags.findIndex((t) => t.id === id);
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
			const page = await api.searchItems({ ...defaultFilter, tag: t.id ?? '' }, 1);
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
			panel = { ...p, busy: false, error: errMsg(e) };
		}
	}

	function deleteLines(p: Extract<Panel, { kind: 'delete' }>): string[] {
		const n = ruleCounts.get(p.tag.id ?? '') ?? 0;
		const items = p.items === null ? 'every item that has it' : `${p.items} ${p.items === 1 ? 'item' : 'items'}`;
		return [
			`This also deletes its ${n} ${n === 1 ? 'rule' : 'rules'} and removes it from ${items}.`,
			'Use edit (e) instead to rename or recolor it.'
		];
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
	view="tags"
	items={tags}
	key={(t) => t.id ?? ''}
	bind:cursor
	panel={panel === null ? null : panel.kind === 'delete' ? 'confirm' : 'form'}
	{tools}
	note={note || (metadata.error ? 'error: ' + metadata.error : '')}
	counts={{ sources: metadata.sources.length, tags: tags.length, rules: rules.length }}
	{loaded}
	{onaction}
>
	{#snippet row(t)}
		<span class="chip">[<span style:color={safeColor(t.color)}>{oneLine(t.name)}</span>]</span>
		<span class="color">{safeColor(t.color) ?? 'no color'}</span>
		<span class="rules">{ruleCounts.get(t.id ?? '') ?? 0} rules</span>
	{/snippet}
	{#snippet panelContent()}
		{#if panel?.kind === 'add'}
			<TagFormPanel initial={tagForm()} editing={false} onsave={save} oncancel={() => (panel = null)} />
		{:else if panel?.kind === 'edit'}
			{#key panel.tag.id}
				<TagFormPanel initial={tagForm(panel.tag)} editing={true} onsave={save} oncancel={() => (panel = null)} />
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
