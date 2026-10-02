<!--
	Sources: add, edit, delete, enable/disable and refresh. Each row shows
	when the source was last fetched, when it is next due (last fetch plus
	its interval), and its last fetch error.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import * as api from '$lib/api';
	import ConfirmPanel from '$lib/ConfirmPanel.svelte';
	import { defaultFilter } from '$lib/filter';
	import { formatInterval, sourceAddBody, sourceForm, sourcePatchBody, type SourceForm } from '$lib/forms';
	import { shortDuration } from '$lib/format';
	import type { ManageAction, Tool } from '$lib/keymap';
	import ManageView from '$lib/ManageView.svelte';
	import { nextFetch } from '$lib/meta';
	import { metadata } from '$lib/metadata.svelte';
	import { oneLine, safeColor } from '$lib/sanitize';
	import SourceFormPanel from '$lib/SourceForm.svelte';
	import type { Source } from '$lib/types';

	const POLL_MS = 5000;

	const tools: Tool[] = [
		{ type: 'add', label: 'add', key: 'a' },
		{ type: 'edit', label: 'edit', key: 'e' },
		{ type: 'delete', label: 'delete', key: 'x' },
		{ type: 'toggle', label: 'on/off', key: 'space' },
		{ type: 'refresh', label: 'refresh', key: 'r' }
	];

	type Panel =
		| { kind: 'add' }
		| { kind: 'edit'; source: Source }
		| { kind: 'delete'; source: Source; items: number | null; rules: number | null; error: string; busy: boolean };

	let cursor = $state(0);
	// Raw: panels are replaced whole, and async steps compare them by identity.
	let panel: Panel | null = $state.raw(null);
	let loaded = $state(false);
	let now = $state(Date.now());
	let note = $state('');
	let noteTimer: ReturnType<typeof setTimeout> | undefined;

	const sources = $derived(metadata.sources);
	const selected = $derived(sources[cursor]);

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
		const i = sources.findIndex((s) => s.id === id);
		if (i >= 0) cursor = i;
	}

	/** Fetch results land in the list a moment after a refresh. */
	function reloadSoon() {
		for (const ms of [1500, 4000]) setTimeout(() => void metadata.reloadSources(), ms);
	}

	// ── Actions ───────────────────────────────────────────────

	async function save(f: SourceForm) {
		if (panel?.kind === 'edit') {
			const orig = panel.source;
			const patch = sourcePatchBody(orig, f);
			if (Object.keys(patch).length > 0) {
				await api.updateSource(orig.id ?? '', patch);
				flash(`saved ${oneLine(f.name)}`);
				if ('url' in patch || 'enabled' in patch) reloadSoon();
			}
			panel = null;
			await reload();
			selectId(orig.id);
		} else {
			const created = await api.addSource(sourceAddBody(f));
			panel = null;
			flash(`added ${oneLine(created.name)}${created.enabled ? ', fetching…' : ''}`);
			await reload();
			selectId(created.id);
			reloadSoon();
		}
	}

	async function toggle(s: Source) {
		try {
			const updated = await api.updateSource(s.id ?? '', { enabled: !s.enabled });
			flash(`${updated.enabled ? 'enabled' : 'disabled'} ${oneLine(s.name)}`);
			await reload();
			if (updated.enabled) reloadSoon();
		} catch (e) {
			flash('error: ' + errMsg(e));
		}
	}

	async function refresh(s: Source) {
		try {
			await api.refresh(s.id);
			flash(`refreshing ${oneLine(s.name)}`);
			reloadSoon();
		} catch (e) {
			flash('error: ' + errMsg(e));
		}
	}

	async function askDelete(s: Source) {
		const p: Panel = { kind: 'delete', source: s, items: null, rules: null, error: '', busy: false };
		panel = p;
		const [items, rules] = await Promise.allSettled([
			api.searchItems({ ...defaultFilter, source: s.id ?? '' }, 1),
			api.listRules()
		]);
		if (panel !== p) return;
		panel = {
			...p,
			items: items.status === 'fulfilled' ? items.value.total : null,
			rules: rules.status === 'fulfilled' ? rules.value.filter((r) => r.source_id === s.id).length : null
		};
	}

	async function confirmDelete() {
		if (panel?.kind !== 'delete' || panel.busy) return;
		const p = panel;
		panel = { ...p, busy: true, error: '' };
		try {
			await api.removeSource(p.source.id ?? '');
			panel = null;
			flash(`deleted ${oneLine(p.source.name)}`);
			await reload();
		} catch (e) {
			if (api.isNotFound(e)) {
				// Another client removed it first: the goal is met, and the list
				// is stale, so refresh it instead of leaving the error up.
				panel = null;
				flash(`already deleted ${oneLine(p.source.name)}`);
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
				if (selected) panel = { kind: 'edit', source: selected };
				return;
			case 'delete':
				if (selected) void askDelete(selected);
				return;
			case 'toggle':
				if (selected) void toggle(selected);
				return;
			case 'refresh':
				if (selected) void refresh(selected);
				return;
			case 'confirm':
				return void confirmDelete();
			case 'cancel':
				panel = null;
				return;
		}
	}

	function plural(n: number | null, one: string, many: string): string {
		if (n === null) return `its ${many} (count unavailable)`;
		return `its ${n} ${n === 1 ? one : many}`;
	}

	// ── Row text ──────────────────────────────────────────────

	function lastText(s: Source): string {
		const lf = s.last_fetch ? Date.parse(s.last_fetch) : NaN;
		return Number.isNaN(lf) ? 'never' : shortDuration(now - lf) + ' ago';
	}

	function nextText(s: Source): string {
		const n = nextFetch(s, now);
		if (n === null) return 'off';
		return n <= now ? 'due' : 'in ' + shortDuration(n - now);
	}

	onMount(() => {
		void reload();
		const poll = setInterval(() => void metadata.reloadSources(), POLL_MS);
		const clock = setInterval(() => (now = Date.now()), 1000);
		return () => {
			clearInterval(poll);
			clearInterval(clock);
			clearTimeout(noteTimer);
		};
	});
</script>

<svelte:head><title>sources · nyttig</title></svelte:head>

<ManageView
	view="sources"
	items={sources}
	key={(s) => s.id ?? ''}
	bind:cursor
	panel={panel === null ? null : panel.kind === 'delete' ? 'confirm' : 'form'}
	{tools}
	note={note || (metadata.error ? 'error: ' + metadata.error : '')}
	counts={{ sources: sources.length, tags: metadata.tags.length }}
	{loaded}
	{onaction}
>
	{#snippet row(s)}
		<span class="on" class:off={!s.enabled} title={s.enabled ? 'enabled' : 'disabled'}
			>{s.enabled ? '●' : '○'}</span
		>
		<span class="chip">[<span style:color={safeColor(s.color)}>{oneLine(s.abbreviation || s.name)}</span>]</span>
		<span class="name">{oneLine(s.name)}</span>
		<span class="every" title="refresh interval">{formatInterval(s.refresh_sec)}</span>
		<span class="last"><span class="k">{'last '}</span>{lastText(s)}</span>
		<span class="next"><span class="k">{'next '}</span>{nextText(s)}</span>
		{#if s.fetch_error}
			<span class="ferr" title={s.fetch_error} data-testid="fetch-error">{oneLine(s.fetch_error)}</span>
		{:else}
			<span class="url">{oneLine(s.url)}</span>
		{/if}
	{/snippet}
	{#snippet panelContent()}
		{#if panel?.kind === 'add'}
			<SourceFormPanel initial={sourceForm()} editing={false} onsave={save} oncancel={() => (panel = null)} />
		{:else if panel?.kind === 'edit'}
			{#key panel.source.id}
				<SourceFormPanel
					initial={sourceForm(panel.source)}
					editing={true}
					onsave={save}
					oncancel={() => (panel = null)}
				/>
			{/key}
		{:else if panel?.kind === 'delete'}
			<ConfirmPanel
				title={`delete source ${oneLine(panel.source.name)}?`}
				lines={[
					`This also deletes ${plural(panel.items, 'item', 'items')} and ${plural(panel.rules, 'rule', 'rules')} for this source.`
				]}
				busy={panel.busy}
				error={panel.error}
				onconfirm={() => void confirmDelete()}
				oncancel={() => (panel = null)}
			/>
		{/if}
	{/snippet}
	{#snippet empty()}
		no sources yet — press <b>a</b> to add one
	{/snippet}
</ManageView>

<style>
	.on {
		flex: none;
		width: 1ch;
		color: var(--unviewed);
	}
	.on.off {
		color: var(--dim);
	}
	.chip {
		flex: none;
		color: var(--dim);
	}
	.name {
		flex: 0 1 28ch;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.every {
		flex: none;
		width: 6ch;
		color: var(--dim);
		text-align: right;
	}
	.last {
		flex: none;
		width: 14ch;
	}
	.next {
		flex: none;
		width: 12ch;
	}
	.k {
		color: var(--dim);
	}
	.url,
	.ferr {
		flex: 1 1 0;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.url {
		color: var(--accent);
	}
	.ferr {
		color: var(--error);
	}
	b {
		color: var(--fg-strong);
		font-weight: normal;
	}
	@media (max-width: 719.98px) {
		.name {
			flex: 1 1 0;
		}
		.every,
		.last {
			display: none;
		}
		.next {
			width: auto;
		}
		.url,
		.ferr {
			flex: 1 0 100%;
			padding-left: 2ch;
		}
	}
</style>
