<!--
	Tag rules: add, edit and delete, with a live preview of what a pattern
	matches. There is no UpdateTagRule: an edit adds the new rule and then
	removes the old one. Removing a rule never touches items it already
	tagged, and adding first means an invalid pattern loses nothing.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import * as api from '$lib/api';
	import ConfirmPanel from '$lib/ConfirmPanel.svelte';
	import { ruleBody, ruleForm, sameRule, type RuleForm } from '$lib/forms';
	import type { ManageAction, Tool } from '$lib/keymap';
	import ManageView from '$lib/ManageView.svelte';
	import { sourceDisplays, tagDisplays } from '$lib/meta';
	import { metadata } from '$lib/metadata.svelte';
	import RuleFormPanel from '$lib/RuleForm.svelte';
	import { oneLine, safeColor } from '$lib/sanitize';
	import type { TagRule } from '$lib/types';

	const tools: Tool[] = [
		{ type: 'add', label: 'add', key: 'a' },
		{ type: 'edit', label: 'edit', key: 'e' },
		{ type: 'delete', label: 'delete', key: 'x' }
	];

	type Panel =
		| { kind: 'add' }
		| { kind: 'edit'; rule: TagRule }
		| { kind: 'delete'; rule: TagRule; error: string; busy: boolean };

	let cursor = $state(0);
	// Raw: panels are replaced whole, and async steps compare them by identity.
	let panel: Panel | null = $state.raw(null);
	let loaded = $state(false);
	let rules: TagRule[] = $state.raw([]);
	let note = $state('');
	let noteTimer: ReturnType<typeof setTimeout> | undefined;

	const selected = $derived(rules[cursor]);
	const srcMeta = $derived(sourceDisplays(metadata.sources));
	const tagMeta = $derived(tagDisplays(metadata.tags));

	function flash(msg: string) {
		note = msg;
		clearTimeout(noteTimer);
		noteTimer = setTimeout(() => (note = ''), 6000);
	}

	function errMsg(e: unknown): string {
		return e instanceof Error ? e.message : String(e);
	}

	async function reload() {
		const [r] = await Promise.allSettled([api.listRules(), metadata.reload()]);
		if (r.status === 'fulfilled') rules = r.value;
		else flash('error: ' + errMsg(r.reason));
		loaded = true;
	}

	function selectId(id: string | undefined) {
		const i = rules.findIndex((r) => r.id === id);
		if (i >= 0) cursor = i;
	}

	function describe(r: TagRule): string {
		const tag = tagMeta.get(r.tag_id ?? '')?.name || r.tag_name || '?';
		return `[${oneLine(tag)}] ${r.field || 'both'} /${oneLine(r.pattern)}/`;
	}

	async function save(f: RuleForm) {
		if (panel?.kind === 'edit') {
			const old = panel.rule;
			if (sameRule(old, f)) {
				panel = null;
				return;
			}
			// Add first: if the new rule is rejected, the old one is kept.
			const created = await api.addRule(ruleBody(f));
			panel = null;
			try {
				await api.removeRule(old.id ?? '');
				flash(`saved ${describe(created)}`);
			} catch (e) {
				flash(`error: saved the new rule, but removing the old one failed (${errMsg(e)}); delete it with x`);
			}
			await reload();
			selectId(created.id);
		} else {
			const created = await api.addRule(ruleBody(f));
			panel = null;
			flash(`added ${describe(created)}`);
			await reload();
			selectId(created.id);
		}
	}

	async function confirmDelete() {
		if (panel?.kind !== 'delete' || panel.busy) return;
		const p = panel;
		panel = { ...p, busy: true, error: '' };
		try {
			await api.removeRule(p.rule.id ?? '');
			panel = null;
			flash(`deleted ${describe(p.rule)}`);
			await reload();
		} catch (e) {
			if (api.isNotFound(e)) {
				// Another client removed it first: the goal is met, and the list
				// is stale, so refresh it instead of leaving the error up.
				panel = null;
				flash(`already deleted ${describe(p.rule)}`);
				await reload();
				return;
			}
			panel = { ...p, busy: false, error: errMsg(e) };
		}
	}

	function onaction(a: ManageAction) {
		switch (a.type) {
			case 'add':
				if (metadata.tags.length === 0) {
					flash('error: a rule needs a tag; add one first with :tags');
					return;
				}
				panel = { kind: 'add' };
				return;
			case 'edit':
				if (selected) panel = { kind: 'edit', rule: selected };
				return;
			case 'delete':
				if (selected) panel = { kind: 'delete', rule: selected, error: '', busy: false };
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

<svelte:head><title>rules · nyttig</title></svelte:head>

<ManageView
	page="rules"
	items={rules}
	key={(r) => r.id ?? ''}
	bind:cursor
	panel={panel === null ? null : panel.kind === 'delete' ? 'confirm' : 'form'}
	{tools}
	note={note || (metadata.error ? 'error: ' + metadata.error : '')}
	counts={{ sources: metadata.sources.length, tags: metadata.tags.length, rules: rules.length, views: metadata.views.length, assessors: metadata.assessors.length, digests: metadata.series.length }}
	{loaded}
	{onaction}
>
	{#snippet row(r)}
		{@const tag = tagMeta.get(r.tag_id ?? '')}
		{@const src = r.source_id ? srcMeta.get(r.source_id) : undefined}
		<span class="chip">[<span style:color={tag?.color}>{oneLine(tag?.name || r.tag_name)}</span>]</span>
		<span class="field">{r.field || 'both'}</span>
		<span class="src"
			><span class="k">src:</span>{#if r.source_id}<span style:color={safeColor(src?.color)}
					>{oneLine(src?.label) || '#' + r.source_id}</span
				>{:else}all{/if}</span
		>
		<span class="prio"><span class="k">{'prio '}</span>{r.priority ?? 0}</span>
		<span class="pattern">{oneLine(r.pattern)}</span>
	{/snippet}
	{#snippet panelContent()}
		{#if panel?.kind === 'add' || panel?.kind === 'edit'}
			{@const rule = panel.kind === 'edit' ? panel.rule : undefined}
			{#key rule?.id}
				<RuleFormPanel
					initial={ruleForm(rule, selected?.tag_id ?? metadata.tags[0]?.id ?? '')}
					editing={!!rule}
					tags={metadata.tags}
					sources={metadata.sources}
					sourceLabels={srcMeta}
					onsave={save}
					oncancel={() => (panel = null)}
				/>
			{/key}
		{:else if panel?.kind === 'delete'}
			<ConfirmPanel
				title={`delete rule ${describe(panel.rule)}?`}
				lines={['Items it already tagged keep the tag.']}
				busy={panel.busy}
				error={panel.error}
				onconfirm={() => void confirmDelete()}
				oncancel={() => (panel = null)}
			/>
		{/if}
	{/snippet}
	{#snippet empty()}
		no rules yet — press <b>a</b> to add one
	{/snippet}
</ManageView>

<style>
	.chip {
		flex: 0 1 18ch;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		color: var(--dim);
	}
	.field {
		flex: none;
		width: 11ch;
		color: var(--dim);
	}
	.src {
		flex: none;
		width: 12ch;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.prio {
		flex: none;
		width: 8ch;
	}
	.k {
		color: var(--dim);
	}
	.pattern {
		flex: 1 1 0;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		color: var(--fg-strong);
	}
	b {
		color: var(--fg-strong);
		font-weight: normal;
	}
	@media (max-width: 719.98px) {
		.field,
		.prio {
			display: none;
		}
		.src {
			width: auto;
			margin-left: auto;
		}
		.pattern {
			flex: 1 0 100%;
			padding-left: 1ch;
		}
	}
</style>
