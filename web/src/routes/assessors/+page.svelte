<!--
	Assessors: the systems that score items (a model, a CVE reader, you).
	Add, edit (name, what the score means, color) and delete, which takes all
	of the assessor's assessments with it. Names and descriptions are
	untrusted text and are only rendered as text.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import * as api from '$lib/api';
	import AssessorFormPanel from '$lib/AssessorForm.svelte';
	import ConfirmPanel from '$lib/ConfirmPanel.svelte';
	import { assessorAddBody, assessorForm, assessorPatchBody, type AssessorForm } from '$lib/forms';
	import { assessorDigestUsage, assessorDigestWarning } from '$lib/digests';
	import type { ManageAction, Tool } from '$lib/keymap';
	import ManageView from '$lib/ManageView.svelte';
	import { metadata } from '$lib/metadata.svelte';
	import { oneLine, safeColor } from '$lib/sanitize';
	import type { Assessor } from '$lib/types';
	import { usageWarning, viewsUsing } from '$lib/views';

	const tools: Tool[] = [
		{ type: 'add', label: 'add', key: 'a' },
		{ type: 'edit', label: 'edit', key: 'e' },
		{ type: 'delete', label: 'delete', key: 'x' }
	];

	type Panel =
		| { kind: 'add' }
		| { kind: 'edit'; assessor: Assessor }
		| { kind: 'delete'; assessor: Assessor; error: string; busy: boolean };

	let cursor = $state(0);
	// Raw: panels are replaced whole, and async steps compare them by identity.
	let panel: Panel | null = $state.raw(null);
	let loaded = $state(false);
	let note = $state('');
	let noteTimer: ReturnType<typeof setTimeout> | undefined;

	const assessors = $derived(metadata.assessors);
	const selected = $derived(assessors[cursor]);

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
		const i = assessors.findIndex((a) => a.id === id);
		if (i >= 0) cursor = i;
	}

	async function save(f: AssessorForm) {
		if (panel?.kind === 'edit') {
			const orig = panel.assessor;
			const patch = assessorPatchBody(orig, f);
			if (Object.keys(patch).length > 0) {
				await api.updateAssessor(orig.id ?? '', patch);
				flash(`saved ${oneLine(f.name)}`);
			}
			panel = null;
			await reload();
			selectId(orig.id);
		} else {
			const created = await api.addAssessor(assessorAddBody(f));
			panel = null;
			flash(`added ${oneLine(created.name)}`);
			await reload();
			selectId(created.id);
		}
	}

	async function confirmDelete() {
		if (panel?.kind !== 'delete' || panel.busy) return;
		const p = panel;
		panel = { ...p, busy: true, error: '' };
		try {
			await api.removeAssessor(p.assessor.id ?? '');
			panel = null;
			flash(`deleted ${oneLine(p.assessor.name)}`);
			await reload();
		} catch (e) {
			if (api.isNotFound(e)) {
				// Another client removed it first: the goal is met.
				panel = null;
				flash(`already deleted ${oneLine(p.assessor.name)}`);
				await reload();
				return;
			}
			panel = { ...p, busy: false, error: errMsg(e) };
		}
	}

	function deleteLines(a: Assessor): string[] {
		const lines = ['This also deletes every assessment (score and note) it has written.'];
		const w = usageWarning('assessor', viewsUsing({ assessor: a.id ?? '' }, metadata.views).length);
		if (w) lines.push(w);
		const d = assessorDigestWarning(assessorDigestUsage(metadata.series, a.id ?? ''));
		if (d) lines.push(d);
		lines.push('Use edit (e) instead to rename or recolor it.');
		return lines;
	}

	function onaction(a: ManageAction) {
		switch (a.type) {
			case 'add':
				panel = { kind: 'add' };
				return;
			case 'edit':
				if (selected) panel = { kind: 'edit', assessor: selected };
				return;
			case 'delete':
				if (selected) panel = { kind: 'delete', assessor: selected, error: '', busy: false };
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

<svelte:head><title>assessors · nyttig</title></svelte:head>

<ManageView
	page="assessors"
	items={assessors}
	key={(a) => a.id ?? ''}
	bind:cursor
	panel={panel === null ? null : panel.kind === 'delete' ? 'confirm' : 'form'}
	{tools}
	note={note || (metadata.error ? 'error: ' + metadata.error : '')}
	counts={{
		sources: metadata.sources.length,
		tags: metadata.tags.length,
		views: metadata.views.length,
		assessors: assessors.length,
		digests: metadata.series.length
	}}
	{loaded}
	{onaction}
>
	{#snippet row(a)}
		<span class="chip">[<span style:color={safeColor(a.color)}>{oneLine(a.name)}</span>]</span>
		<span class="color">{safeColor(a.color) ?? 'no color'}</span>
		<span class="desc" data-testid="assessor-scale">{oneLine(a.description) || 'no description of its scale'}</span>
	{/snippet}
	{#snippet panelContent()}
		{#if panel?.kind === 'add'}
			<AssessorFormPanel initial={assessorForm()} editing={false} onsave={save} oncancel={() => (panel = null)} />
		{:else if panel?.kind === 'edit'}
			{#key panel.assessor.id}
				<AssessorFormPanel
					initial={assessorForm(panel.assessor)}
					editing={true}
					onsave={save}
					oncancel={() => (panel = null)}
				/>
			{/key}
		{:else if panel?.kind === 'delete'}
			<ConfirmPanel
				title={`delete assessor ${oneLine(panel.assessor.name)}?`}
				lines={deleteLines(panel.assessor)}
				busy={panel.busy}
				error={panel.error}
				onconfirm={() => void confirmDelete()}
				oncancel={() => (panel = null)}
			/>
		{/if}
	{/snippet}
	{#snippet empty()}
		no assessors yet — press <b>a</b> to add one, or <code>nyttig add-assessor</code>
	{/snippet}
</ManageView>

<style>
	.chip {
		flex: 0 1 24ch;
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
	.desc {
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
