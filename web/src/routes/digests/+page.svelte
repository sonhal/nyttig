<!--
	Digests: documents an assessor wrote about many items, kept in series.
	A list of series (grouped by assessor), the history of the selected
	series, and the digest being read with the items it is based on and the
	earlier digests it used as input. The URL is the source of truth:
	/digests?series=<id>&digest=<id>; no digest means the newest of the
	series, no series the first one.

	Everything here is untrusted text (an LLM wrote it, and it can repeat the
	markup of the feed it read): titles, names and the body are rendered as
	text only (the body by the Markdown renderer, which builds elements and
	text nodes and nothing else), item links go through safeLink.
-->
<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { onMount, tick, untrack } from 'svelte';
	import * as api from '$lib/api';
	import CommandLine from '$lib/CommandLine.svelte';
	import { CommandLine as CommandLineState, execute, offFeedHost } from '$lib/commandline.svelte';
	import ConfirmPanel from '$lib/ConfirmPanel.svelte';
	import {
		adjacentDigest,
		adjacentSeries,
		currentSeries,
		deleteSeriesLines,
		digestsHref,
		formatLatest,
		formatPeriod,
		groupSeries,
		inputId,
		mergeDigests,
		moveWithinAssessor,
		selectionFromParams,
		seriesLabel
	} from '$lib/digests';
	import { seriesForm, seriesPatchBody, type SeriesForm } from '$lib/forms';
	import { formatDate } from '$lib/format';
	import Help from '$lib/Help.svelte';
	import Markdown from '$lib/Markdown.svelte';
	import { digestsSections } from '$lib/help';
	import { digestsKeyAction, type DigestsAction, type ManageMode, type Tool } from '$lib/keymap';
	import { metadata } from '$lib/metadata.svelte';
	import PageTabs from '$lib/PageTabs.svelte';
	import { oneLine, safeColor, safeLink } from '$lib/sanitize';
	import SeriesFormPanel from '$lib/SeriesForm.svelte';
	import type { Digest, DigestSeries } from '$lib/types';

	/** Digests per page of the history. */
	const PAGE_SIZE = 20;
	/** How many pages a step looks through for a digest that is not in the loaded history. */
	const MAX_LOCATE_PAGES = 5;

	const tools: Tool[] = [
		{ type: 'edit', label: 'edit', key: 'e' },
		{ type: 'delete', label: 'delete', key: 'x' },
		{ type: 'moveUp', label: 'up', key: 'K' },
		{ type: 'moveDown', label: 'down', key: 'J' }
	];

	type Panel =
		| { kind: 'edit'; series: DigestSeries }
		| { kind: 'delete'; series: DigestSeries; error: string; busy: boolean };

	// ── Selection (the URL) ───────────────────────────────────

	const sel = $derived(selectionFromParams(page.url.searchParams));
	const allSeries = $derived(metadata.series);
	const current = $derived(currentSeries(allSeries, sel.series));
	const seriesId = $derived(current?.id ?? '');
	const groups = $derived(groupSeries(allSeries));
	const assessorColor = $derived(new Map(metadata.assessors.map((a) => [a.id ?? '', safeColor(a.color)])));
	/** A digest with no series in the URL (an input link): its series is only known once it is fetched. */
	const resolving = $derived(sel.digest !== '' && sel.series === '');

	// ── State ─────────────────────────────────────────────────

	let loaded = $state(false);
	let history: Digest[] = $state.raw([]);
	let hasMore = $state(false);
	let historyLoaded = $state(false);
	let loadingOlder = $state(false);
	let historyError = $state('');
	let reading = $state.raw<Digest | null>(null);
	let readingError = $state('');
	let panel = $state.raw<Panel | null>(null);
	let note = $state('');
	let noteTimer: ReturnType<typeof setTimeout> | undefined;
	let reloadTick = $state(0);
	/** On a phone: which of the three screens shows. */
	let screen: 'series' | 'history' | 'reading' = $state('series');
	let helpOpen = $state(false);
	let readingEl: HTMLElement | undefined = $state();
	let historyEl: HTMLElement | undefined = $state();
	let panelEl: HTMLElement | undefined = $state();

	const cl = new CommandLineState();
	const mode: ManageMode = $derived(cl.open ? 'command' : helpOpen ? 'help' : panel ? (panel.kind === 'delete' ? 'confirm' : 'form') : 'normal');
	const wantDigest = $derived(sel.digest || (historyLoaded ? (history[0]?.id ?? '') : ''));

	function flash(msg: string) {
		note = msg;
		clearTimeout(noteTimer);
		noteTimer = setTimeout(() => (note = ''), 5000);
	}

	function errMsg(e: unknown): string {
		return e instanceof Error ? e.message : String(e);
	}

	// ── Loading ───────────────────────────────────────────────

	let historyEpoch = 0;

	async function loadHistory(id: string) {
		const epoch = ++historyEpoch;
		history = [];
		hasMore = false;
		historyError = '';
		loadingOlder = false;
		historyLoaded = false;
		if (!id) {
			historyLoaded = true;
			return;
		}
		try {
			const p = await api.listDigests(id, { limit: PAGE_SIZE });
			if (epoch !== historyEpoch) return;
			history = p.digests;
			hasMore = p.hasMore;
		} catch (e) {
			if (epoch !== historyEpoch) return;
			historyError = errMsg(e);
		}
		historyLoaded = true;
	}

	/** Loads the next older page of the history; true when it added digests. */
	async function loadOlder(): Promise<boolean> {
		const last = history[history.length - 1];
		if (!hasMore || loadingOlder || !last?.id || !seriesId) return false;
		const epoch = historyEpoch;
		loadingOlder = true;
		try {
			const p = await api.listDigests(seriesId, { before: last.id, limit: PAGE_SIZE });
			if (epoch !== historyEpoch) return false;
			history = mergeDigests(history, p.digests);
			hasMore = p.hasMore;
			return p.digests.length > 0;
		} catch (e) {
			if (epoch === historyEpoch) flash('error: ' + errMsg(e));
			return false;
		} finally {
			if (epoch === historyEpoch) loadingOlder = false;
		}
	}

	let readEpoch = 0;

	async function loadReading(id: string) {
		const epoch = ++readEpoch;
		readingError = '';
		if (!id) {
			reading = null;
			return;
		}
		try {
			const d = await api.getDigest(id);
			if (epoch !== readEpoch) return;
			reading = d;
			void tick().then(() => readingEl?.scrollTo({ top: 0 }));
			// An input link names only the digest; the URL then learns its series.
			if (sel.digest === id && d.series_id && d.series_id !== sel.series) {
				void goto(digestsHref(d.series_id, id), { replaceState: true, noScroll: true, keepFocus: true });
			}
		} catch (e) {
			if (epoch !== readEpoch) return;
			reading = null;
			readingError = api.isNotFound(e) ? 'that digest does not exist (any more)' : errMsg(e);
		}
	}

	async function reload() {
		await metadata.reload();
		loaded = true;
	}

	// The history follows the series (and "r"); the digest follows the URL,
	// or the newest of the history.
	$effect(() => {
		const id = resolving ? '' : seriesId;
		void reloadTick;
		untrack(() => void loadHistory(id));
	});

	$effect(() => {
		const id = wantDigest;
		untrack(() => void loadReading(id));
	});

	// A phone shows one screen at a time; the URL says which one a link goes to.
	$effect(() => {
		const s = selectionFromParams(page.url.searchParams);
		untrack(() => (screen = s.digest ? 'reading' : s.series ? 'history' : 'series'));
	});

	// ── Navigation ────────────────────────────────────────────

	function go(href: string) {
		void goto(href, { noScroll: true, keepFocus: true });
	}

	function selectSeries(id: string) {
		screen = 'history';
		go(digestsHref(id));
	}

	function selectDigest(id: string) {
		screen = 'reading';
		go(digestsHref(seriesId, id));
	}

	/** Steps to the next older (1) or newer (-1) digest, loading older history when it runs out. */
	async function stepDigest(dir: 1 | -1) {
		const cur = reading?.id ?? wantDigest;
		if (!cur) return;
		// A digest opened by a link may be further back than what is loaded.
		for (let pages = 0; !history.some((d) => d.id === cur) && hasMore && pages < MAX_LOCATE_PAGES; pages++) {
			if (!(await loadOlder())) break;
		}
		let next = adjacentDigest(history, cur, dir);
		if (!next && dir === 1 && (await loadOlder())) next = adjacentDigest(history, cur, 1);
		if (next?.id) selectDigest(next.id);
	}

	function stepSeries(dir: 1 | -1) {
		const next = adjacentSeries(allSeries, seriesId, dir);
		if (next?.id && next.id !== seriesId) selectSeries(next.id);
	}

	function scrollReading(dir: 1 | -1) {
		if (readingEl) readingEl.scrollBy({ top: dir * Math.max(40, readingEl.clientHeight / 2) });
	}

	function back() {
		screen = screen === 'reading' ? 'history' : 'series';
	}

	// ── Series management ─────────────────────────────────────

	async function save(f: SeriesForm) {
		if (panel?.kind !== 'edit') return;
		const orig = panel.series;
		const patch = seriesPatchBody(orig, f);
		if (Object.keys(patch).length > 0) {
			await api.updateDigestSeries(orig.id ?? '', patch);
			flash(`saved ${oneLine(f.name)}`);
		}
		panel = null;
		await reload();
	}

	async function confirmDelete() {
		if (panel?.kind !== 'delete' || panel.busy) return;
		const p = panel;
		panel = { ...p, busy: true, error: '' };
		const gone = () => {
			panel = null;
			if (seriesId === p.series.id) go(digestsHref());
		};
		try {
			await api.removeDigestSeries(p.series.id ?? '');
			gone();
			flash(`deleted ${seriesLabel(p.series)}`);
			await reload();
		} catch (e) {
			if (api.isNotFound(e)) {
				// Another client removed it first: the goal is met.
				gone();
				flash(`already deleted ${seriesLabel(p.series)}`);
				await reload();
				return;
			}
			panel = { ...p, busy: false, error: errMsg(e) };
		}
	}

	/** K/J: one place up or down among the assessor's own series. */
	async function move(dir: 1 | -1) {
		if (!current?.id) return;
		const ids = moveWithinAssessor(allSeries, current.id, dir);
		if (!ids) return;
		try {
			metadata.series = await api.reorderDigestSeries(ids);
		} catch (e) {
			flash('error: ' + errMsg(e));
			await reload();
		}
	}

	// ── Keys ──────────────────────────────────────────────────

	async function refreshCmd(id?: string) {
		try {
			await api.refresh(id);
			flash(id ? 'refreshing source' : 'refreshing all sources');
		} catch (e) {
			flash('error: refresh failed: ' + errMsg(e));
		}
	}

	const host = { ...offFeedHost(flash, (id) => void refreshCmd(id)), help: () => (helpOpen = true) };

	function closeHelp() {
		helpOpen = false;
	}

	function run(a: DigestsAction) {
		switch (a.type) {
			case 'olderDigest':
				return void stepDigest(1);
			case 'newerDigest':
				return void stepDigest(-1);
			case 'newestDigest': {
				const d = history[0];
				if (d?.id) selectDigest(d.id);
				return;
			}
			case 'oldestDigest': {
				const d = history[history.length - 1];
				if (d?.id) selectDigest(d.id);
				return;
			}
			case 'nextSeries':
				return stepSeries(1);
			case 'prevSeries':
				return stepSeries(-1);
			case 'scroll':
				return scrollReading(a.dir);
			case 'openInput': {
				const id = inputId(reading ?? undefined, a.n);
				if (id) {
					screen = 'reading';
					go(digestsHref('', id));
				}
				return;
			}
			case 'reload':
				reloadTick++;
				return void reload();
			case 'edit':
				if (current) panel = { kind: 'edit', series: current };
				return;
			case 'delete':
				if (current) panel = { kind: 'delete', series: current, error: '', busy: false };
				return;
			case 'moveUp':
				return void move(-1);
			case 'moveDown':
				return void move(1);
			case 'feed':
				return void goto('/' + metadata.feedSearch);
			case 'cancel':
				panel = null;
				return;
			case 'confirm':
				return void confirmDelete();
			case 'openCommand':
				return cl.start();
			case 'runCommand': {
				const cmd = cl.run({
					sources: metadata.sources,
					tags: metadata.tags,
					views: metadata.views,
					assessors: metadata.assessors,
					series: metadata.series
				});
				if (cmd) execute(cmd, host);
				return;
			}
			case 'cancelCommand':
				return cl.cancel();
			case 'completeCommand':
				return cl.complete({
					sources: metadata.sources,
					tags: metadata.tags,
					views: metadata.views,
					assessors: metadata.assessors,
					series: metadata.series
				});
			case 'historyPrev':
				return cl.historyPrev();
			case 'historyNext':
				return cl.historyNext();
			case 'openHelp':
				helpOpen = true;
				return;
			case 'closeHelp':
				return closeHelp();
		}
	}

	function onkeydown(e: KeyboardEvent) {
		const target = e.target as HTMLElement | null;
		// Buttons and links keep Enter and Space: a focused "cancel" must
		// never confirm a delete.
		if (mode !== 'command' && (e.key === 'Enter' || e.key === ' ') && target?.closest('button, a')) return;
		if (mode === 'normal' && target?.closest('input, select, textarea')) return;
		const a = digestsKeyAction(mode, e);
		if (!a) return;
		if ((mode === 'form' || mode === 'confirm') && a.type !== 'cancel' && a.type !== 'confirm') return;
		e.preventDefault();
		run(a);
	}

	// Into a confirmation when it opens, so y/n work without a click.
	$effect(() => {
		if (panel?.kind === 'delete') void tick().then(() => panelEl?.focus({ preventScroll: true }));
	});

	// Keep the selected history row in view while stepping through it.
	$effect(() => {
		const id = reading?.id;
		if (!id) return;
		void tick().then(() => historyEl?.querySelector(`[data-id="${id}"]`)?.scrollIntoView({ block: 'nearest' }));
	});

	onMount(() => {
		void reload();
		return () => {
			clearTimeout(noteTimer);
			historyEpoch++;
			readEpoch++;
		};
	});

	const toolDisabled = $derived(panel !== null || !current);
</script>

<svelte:head><title>digests · nyttig</title></svelte:head>
<svelte:window {onkeydown} />

<div class="digests" data-screen={screen}>
	<PageTabs
		page="digests"
		counts={{
			sources: metadata.sources.length,
			tags: metadata.tags.length,
			views: metadata.views.length,
			assessors: metadata.assessors.length,
			digests: allSeries.length
		}}
	/>

	<div class="layout">
		<aside class="side">
			<section class="pane series" aria-label="series" data-testid="series-list">
				{#if allSeries.length === 0}
					<div class="empty">
						{#if !loaded}loading…{:else}no digest series yet — an assessor program registers them (<code>nyttig add-series</code>){/if}
					</div>
				{/if}
				{#each groups as g (g.assessorId)}
					<div class="group" data-testid="series-group">
						<span class="chip">[<span style:color={assessorColor.get(g.assessorId)}>{oneLine(g.assessorName)}</span>]</span>
					</div>
					{#each g.series as s (s.id)}
						<a
							class="srow"
							class:selected={s.id === seriesId}
							href={digestsHref(s.id)}
							aria-current={s.id === seriesId ? 'true' : undefined}
							data-testid="series-row"
							data-id={s.id}
							onclick={() => (screen = 'history')}
						>
							<span class="sname">{oneLine(s.name)}</span>
							<span class="count">{s.digest_count ?? 0}</span>
							<span class="latest">{formatLatest(s)}</span>
						</a>
					{/each}
				{/each}
			</section>

			<section class="pane history" aria-label="history" bind:this={historyEl} data-testid="history">
				{#if current}
					<div class="head">
						<span class="h-name" data-testid="series-title">{seriesLabel(current)}</span>
						{#if current.description}<span class="h-desc">{oneLine(current.description)}</span>{/if}
					</div>
				{/if}
				{#each history as d (d.id)}
					<a
						class="hrow"
						class:selected={d.id === reading?.id}
						href={digestsHref(seriesId, d.id)}
						aria-current={d.id === reading?.id ? 'true' : undefined}
						data-testid="history-row"
						data-id={d.id}
						onclick={() => (screen = 'reading')}
					>
						<span class="period">{formatPeriod(d.period_start, d.period_end)}</span>
						<span class="dtitle">{oneLine(d.title)}</span>
					</a>
				{/each}
				{#if historyError}
					<div class="empty error" role="alert">{historyError}</div>
				{:else if historyLoaded && current && history.length === 0}
					<div class="empty">no digests in this series yet</div>
				{/if}
				{#if hasMore}
					<button type="button" class="more" disabled={loadingOlder} onclick={() => void loadOlder()} data-testid="load-older"
						>{loadingOlder ? 'loading…' : 'load older'}</button
					>
				{/if}
			</section>
		</aside>

		<main class="pane reading" aria-label="digest" bind:this={readingEl} tabindex="-1" data-testid="reading">
			{#if reading}
				<article>
					<h2 data-testid="digest-title">{oneLine(reading.title)}</h2>
					<div class="meta">
						<span class="chip">[<span style:color={assessorColor.get(reading.assessor_id ?? '')}>{oneLine(reading.assessor_name)}</span>]</span>
						<span data-testid="digest-series">{oneLine(reading.series_name)}</span>
						<span class="dim">·</span>
						<span data-testid="digest-period">{formatPeriod(reading.period_start, reading.period_end)}</span>
						<span class="dim">· updated {formatDate(reading.updated_at)}</span>
					</div>
					<div class="body"><Markdown source={reading.body ?? ''} items={reading.items ?? []} /></div>
					{#if reading.items?.length}
						<h3 data-testid="digest-items-title">Based on {reading.items.length} {reading.items.length === 1 ? 'item' : 'items'}</h3>
						<ul class="items">
							{#each reading.items as it (it.item_id)}
								<li data-testid="digest-item">
									{#if safeLink(it.link)}
										<a href={safeLink(it.link)} target="_blank" rel="noopener noreferrer">{oneLine(it.title) || safeLink(it.link)}</a>
									{:else}
										<span>{oneLine(it.title) || '(untitled)'}</span>
									{/if}
									<span class="dim">{oneLine(it.source_name)}</span>
								</li>
							{/each}
						</ul>
					{/if}
					{#if reading.inputs?.length}
						<h3 data-testid="digest-inputs-title">Inputs</h3>
						<ol class="inputs">
							{#each reading.inputs as inp, i (inp.id)}
								<li>
									<a href={digestsHref('', inp.id ?? '')} data-testid="digest-input" onclick={() => (screen = 'reading')}
										><span class="k">{i < 9 ? i + 1 : ''}</span> {oneLine(inp.title)}</a
									>
									<span class="dim">{oneLine(inp.series_name)} · {(inp.period_end ?? '').slice(0, 10)}</span>
								</li>
							{/each}
						</ol>
					{/if}
				</article>
			{:else if readingError}
				<div class="empty error" role="alert">{readingError}</div>
			{:else}
				<div class="empty">{#if !loaded || !historyLoaded || resolving}loading…{:else if current}select a digest{:else}no digest to show{/if}</div>
			{/if}
		</main>
	</div>

	{#if panel}
		<section class="panel" class:confirm={panel.kind === 'delete'} bind:this={panelEl} tabindex="-1" aria-label={panel.kind === 'delete' ? 'confirm delete' : 'edit'} data-testid="panel">
			{#if panel.kind === 'edit'}
				{#key panel.series.id}
					<SeriesFormPanel initial={seriesForm(panel.series)} onsave={save} oncancel={() => (panel = null)} />
				{/key}
			{:else}
				<ConfirmPanel
					title={`delete series ${seriesLabel(panel.series)}?`}
					lines={deleteSeriesLines(panel.series)}
					busy={panel.busy}
					error={panel.error}
					onconfirm={() => void confirmDelete()}
					oncancel={() => (panel = null)}
				/>
			{/if}
		</section>
	{/if}

	{#if note || metadata.error}
		<div class="note" class:error={(note || metadata.error).startsWith('error') || !note} role="status" data-testid="note">
			{note || 'error: ' + metadata.error}
		</div>
	{/if}

	<div class="toolbar" data-testid="toolbar">
		{#if screen !== 'series'}
			<button type="button" class="back" onclick={back} data-testid="back">‹ back</button>
		{/if}
		{#each tools as t (t.type)}
			<button type="button" disabled={toolDisabled} onclick={() => run({ type: t.type as 'edit' | 'delete' | 'moveUp' | 'moveDown' })} data-testid="tool-{t.type}"
				><span class="k">{t.key}</span> {t.label}</button
			>
		{/each}
		<button type="button" class="help" onclick={() => (helpOpen = true)} data-testid="open-help"><span class="k">?</span> help</button>
	</div>
</div>

<CommandLine {cl} />

{#if helpOpen}
	<Help title="Keys: digests" sections={digestsSections(tools.map((t) => t.type))} onclose={closeHelp} />
{/if}

<style>
	.digests {
		display: grid;
		grid-template-rows: auto minmax(0, 1fr) auto auto auto;
		height: 100vh;
		height: 100dvh;
		padding-left: env(safe-area-inset-left);
		padding-right: env(safe-area-inset-right);
	}

	.layout {
		display: grid;
		grid-template-columns: minmax(28ch, 40ch) minmax(0, 1fr);
		min-height: 0;
	}
	.side {
		display: grid;
		grid-template-rows: auto minmax(0, 1fr);
		min-height: 0;
		border-right: 1px solid var(--sel);
	}
	.pane {
		min-height: 0;
		overflow-y: auto;
		overflow-x: hidden;
		overscroll-behavior: contain;
		outline: none;
	}
	.series {
		max-height: 45vh;
		border-bottom: 1px solid var(--sel);
	}

	.group {
		padding: 0 1ch;
		color: var(--dim);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.srow,
	.hrow {
		display: flex;
		align-items: center;
		gap: 1ch;
		height: var(--row-h);
		padding: 0 1ch 0 3ch;
		color: var(--fg);
		text-decoration: none;
		white-space: nowrap;
		overflow: hidden;
	}
	.srow.selected,
	.hrow.selected {
		background: var(--sel);
	}
	@media (hover: hover) {
		.srow:not(.selected):hover,
		.hrow:not(.selected):hover {
			background: #262628;
		}
	}
	.sname,
	.dtitle {
		flex: 1 1 0;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.count {
		flex: none;
		color: var(--dim);
	}
	.latest,
	.period {
		flex: none;
		color: var(--dim);
	}
	.hrow {
		padding-left: 1ch;
	}
	.hrow .period {
		width: 11ch;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.chip {
		color: var(--dim);
	}
	.head {
		display: flex;
		flex-direction: column;
		padding: 0 1ch;
		color: var(--fg-strong);
		line-height: 20px;
	}
	.h-name {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.h-desc {
		color: var(--dim);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.more {
		display: block;
		width: 100%;
		height: var(--row-h);
		background: none;
		border: none;
		color: var(--accent);
		cursor: pointer;
		text-align: left;
		padding: 0 1ch;
	}
	.more:disabled {
		color: var(--dim);
		cursor: default;
	}

	.reading {
		padding: 4px 2ch 16px;
	}
	article {
		max-width: 100ch;
	}
	h2 {
		margin: 4px 0 0;
		font-size: inherit;
		font-weight: normal;
		color: var(--fg-strong);
		overflow-wrap: anywhere;
	}
	h3 {
		margin: 16px 0 0;
		font-size: inherit;
		font-weight: normal;
		color: var(--accent);
	}
	.meta {
		display: flex;
		flex-wrap: wrap;
		gap: 0 1ch;
		color: var(--fg);
	}
	.dim {
		color: var(--dim);
	}
	.body {
		margin-top: 12px;
	}
	.items,
	.inputs {
		margin: 0;
		padding-left: 3ch;
		overflow-wrap: anywhere;
	}
	.inputs {
		list-style: none;
		padding-left: 0;
	}
	.k {
		color: var(--unviewed);
	}
	.empty {
		display: grid;
		place-items: center;
		padding: 1ch;
		min-height: 3em;
		color: var(--dim);
		text-align: center;
	}
	.empty.error {
		color: var(--error);
	}

	.panel {
		max-height: 60vh;
		max-height: 60dvh;
		overflow-y: auto;
		background: #252526;
		border-top: 1px solid var(--sel);
		outline: none;
	}
	.panel.confirm {
		border-top-color: var(--error);
	}
	.note {
		padding: 0 1ch;
		background: var(--bar);
		color: var(--accent);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.note.error {
		color: var(--error);
	}

	.toolbar {
		display: flex;
		align-items: center;
		gap: 2ch;
		height: var(--bar-h);
		padding: 0 1ch;
		background: var(--bar);
		white-space: nowrap;
		overflow: hidden;
	}
	.toolbar button {
		background: none;
		border: none;
		padding: 0;
		cursor: pointer;
		color: var(--fg);
	}
	.toolbar button:disabled {
		color: #5a5a5a;
		cursor: default;
	}
	.toolbar .help {
		margin-left: auto;
		color: var(--dim);
	}
	.toolbar .k {
		color: var(--accent);
	}
	.toolbar button:disabled .k {
		color: inherit;
	}
	.toolbar .back {
		display: none;
	}

	@media (max-width: 719.98px) {
		.layout {
			display: block;
			min-height: 0;
			overflow: hidden;
		}
		.side {
			display: block;
			height: 100%;
			border-right: none;
		}
		.series,
		.history,
		.reading {
			display: none;
			max-height: none;
			height: 100%;
			border-bottom: none;
		}
		[data-screen='reading'] .side {
			display: none;
		}
		[data-screen='series'] .series,
		[data-screen='history'] .history,
		[data-screen='reading'] .reading {
			display: block;
		}
		.srow,
		.hrow {
			padding-left: 8px;
		}
		.hrow {
			flex-wrap: wrap;
			height: auto;
			min-height: var(--row-h);
			padding: 4px 8px;
			white-space: normal;
			gap: 0 1ch;
		}
		.hrow .period {
			width: auto;
		}
		.group {
			padding: 0 8px;
		}
		.reading {
			padding: 8px 8px 16px;
		}
		.items li,
		.inputs li {
			padding: 6px 0;
		}
		.note {
			padding: 4px 8px;
			white-space: normal;
		}
		.toolbar {
			gap: 4px;
			padding: 4px 4px calc(4px + env(safe-area-inset-bottom));
			height: auto;
		}
		.toolbar button {
			flex: 1;
			height: 44px;
			border: 1px solid var(--sel);
			border-radius: 4px;
			font-size: 15px;
		}
		.toolbar .k {
			display: none;
		}
		.toolbar .help {
			margin-left: 0;
		}
		.toolbar .back {
			display: block;
		}
	}
</style>
