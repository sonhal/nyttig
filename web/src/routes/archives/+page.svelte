<!--
	Archives: the saved copies of articles. Filter by status, download a
	copy, retry one (archive the item again) and remove one. Titles, errors
	and notes are untrusted text and are only rendered as text. A copy is
	only ever downloaded: nyttig-api serves it as an attachment, never as a
	page of this app.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import * as api from '$lib/api';
	import { archiveBadge, canDownload, fileHref, formatBytes, inFlight, nextStatusFilter } from '$lib/archives';
	import ConfirmPanel from '$lib/ConfirmPanel.svelte';
	import type { ManageAction, Tool } from '$lib/keymap';
	import ManageView from '$lib/ManageView.svelte';
	import { metadata } from '$lib/metadata.svelte';
	import { oneLine } from '$lib/sanitize';
	import type { Archive } from '$lib/types';

	const POLL_MS = 3000;

	type Panel = { kind: 'delete'; archive: Archive; error: string; busy: boolean };

	let cursor = $state(0);
	let panel: Panel | null = $state.raw(null);
	let loaded = $state(false);
	let archives: Archive[] = $state.raw([]);
	let hasMore = $state(false);
	let status = $state('');
	let note = $state('');
	let noteTimer: ReturnType<typeof setTimeout> | undefined;
	let seq = 0;

	const tools = $derived<Tool[]>([
		{ type: 'filter', label: 'status: ' + (status || 'all'), key: 'f' },
		{ type: 'download', label: 'download', key: 'o' },
		{ type: 'refresh', label: 'retry', key: 'r' },
		{ type: 'delete', label: 'remove', key: 'x' }
	]);
	const selected = $derived(archives[cursor]);

	function flash(msg: string) {
		note = msg;
		clearTimeout(noteTimer);
		noteTimer = setTimeout(() => (note = ''), 5000);
	}

	function errMsg(e: unknown): string {
		return e instanceof Error ? e.message : String(e);
	}

	async function reload() {
		const mine = ++seq;
		try {
			const page = await api.listArchives(status);
			if (mine !== seq) return;
			archives = page.archives;
			hasMore = page.hasMore;
		} catch (e) {
			if (mine === seq) flash('error: ' + errMsg(e));
		}
		if (mine === seq) loaded = true;
	}

	function download(a: Archive) {
		if (!canDownload(a)) return flash('error: nothing is saved to download');
		// A link click, so the browser saves the attachment and the page stays.
		const el = document.createElement('a');
		el.href = fileHref(a.id);
		el.download = '';
		el.click();
	}

	async function retry(a: Archive) {
		if (inFlight(a.status)) return flash('already archiving');
		if (!a.item_id) return flash('error: the item is gone, so it cannot be archived again');
		try {
			await api.archiveItem(a.item_id);
			flash(`archiving ${oneLine(a.title) || 'the item'} again`);
			await reload();
		} catch (e) {
			flash('error: ' + errMsg(e));
		}
	}

	async function confirmDelete() {
		if (panel?.kind !== 'delete' || panel.busy) return;
		const p = panel;
		panel = { ...p, busy: true, error: '' };
		try {
			await api.removeArchive(p.archive.id ?? '');
			panel = null;
			flash(`removed ${oneLine(p.archive.title) || 'the archive'}`);
			await reload();
		} catch (e) {
			if (api.isNotFound(e)) {
				panel = null;
				flash('already removed');
				await reload();
				return;
			}
			panel = { ...p, busy: false, error: errMsg(e) };
		}
	}

	function onaction(a: ManageAction) {
		switch (a.type) {
			case 'filter':
				status = nextStatusFilter(status);
				cursor = 0;
				return void reload();
			case 'download':
				return selected ? download(selected) : undefined;
			case 'refresh':
				return selected ? void retry(selected) : undefined;
			case 'delete':
				if (selected) panel = { kind: 'delete', archive: selected, error: '', busy: false };
				return;
			case 'confirm':
				return void confirmDelete();
			case 'cancel':
				panel = null;
				return;
		}
	}

	onMount(() => {
		void metadata.reload();
		void reload();
		// Queued and running archives finish while the page is open.
		const timer = setInterval(() => {
			if (archives.some((a) => inFlight(a.status))) void reload();
		}, POLL_MS);
		return () => {
			clearInterval(timer);
			clearTimeout(noteTimer);
		};
	});
</script>

<svelte:head><title>archives · nyttig</title></svelte:head>

<ManageView
	page="archives"
	items={archives}
	key={(a) => a.id ?? ''}
	bind:cursor
	panel={panel === null ? null : 'confirm'}
	{tools}
	note={note || (hasMore ? 'showing the newest 200' : '') || (metadata.error ? 'error: ' + metadata.error : '')}
	counts={{
		sources: metadata.sources.length,
		tags: metadata.tags.length,
		views: metadata.views.length,
		assessors: metadata.assessors.length,
		digests: metadata.series.length
	}}
	{loaded}
	{onaction}
>
	{#snippet row(a)}
		{@const badge = archiveBadge({ status: a.status })}
		<span class="status {a.status}" data-testid="archive-status" data-status={a.status}
			>{badge?.glyph ?? '?'} {a.status}</span
		>
		<span class="size">{formatBytes(a.size)}</span>
		<span class="title">{oneLine(a.title) || oneLine(a.link) || '(untitled)'}</span>
		<span class="why" class:err={a.status === 'failed'} data-testid="archive-why"
			>{oneLine(a.error) || oneLine(a.note) || (a.missing_assets ? `${a.missing_assets} files missing` : '')}</span
		>
	{/snippet}
	{#snippet panelContent()}
		{#if panel?.kind === 'delete'}
			<ConfirmPanel
				title={`remove the archive of ${oneLine(panel.archive.title) || 'this item'}?`}
				lines={['This deletes the saved copy. The item stays in the feed.']}
				busy={panel.busy}
				error={panel.error}
				onconfirm={() => void confirmDelete()}
				oncancel={() => (panel = null)}
			/>
		{/if}
	{/snippet}
	{#snippet empty()}
		{#if status}no {status} archives — press <b>f</b> to change the filter
		{:else}no archives yet — press <b>b</b> on an item in the feed, or run <code>nyttig archive</code>{/if}
	{/snippet}
</ManageView>

<style>
	.status {
		flex: none;
		width: 12ch;
		color: var(--accent);
	}
	.status.pending,
	.status.running,
	.status.partial {
		color: var(--dim);
	}
	.status.failed {
		color: var(--error);
	}
	.size {
		flex: none;
		width: 8ch;
		text-align: right;
		color: var(--dim);
	}
	.title {
		flex: 0 1 50%;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.why {
		flex: 1 1 0;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		color: var(--dim);
	}
	.why.err {
		color: var(--error);
	}
	b {
		color: var(--fg-strong);
		font-weight: normal;
	}
</style>
