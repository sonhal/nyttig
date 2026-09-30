<!--
	The feed: filter bar, virtual table and status bar, at parity with the
	TUI. The filter lives in the URL (?q=&source=&tag=&sort=&unviewed=1), so
	views can be bookmarked and the back button works.
-->
<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import * as api from '$lib/api';
	import FeedRow from '$lib/FeedRow.svelte';
	import FilterBar from '$lib/FilterBar.svelte';
	import FilterSheet from '$lib/FilterSheet.svelte';
	import ItemDetail from '$lib/ItemDetail.svelte';
	import StatusBar from '$lib/StatusBar.svelte';
	import VirtualList from '$lib/VirtualList.svelte';
	import { cycleID, filterFromParams, filterQuery, nextSort, sameFilter } from '$lib/filter';
	import CommandLine from '$lib/CommandLine.svelte';
	import { CommandLine as CommandLineState } from '$lib/commandline.svelte';
	import { keyAction, type Action, type Mode } from '$lib/keymap';
	import { fetchTimes, sourceDisplays, tagDisplays } from '$lib/meta';
	import { metadata } from '$lib/metadata.svelte';
	import { markViewed, moveCursor } from '$lib/reducer';
	import { safeLink } from '$lib/sanitize';
	import { FeedStream } from '$lib/stream.svelte';
	import type { Filter, Item } from '$lib/types';
	import { ViewTracker } from '$lib/viewed';

	const METADATA_INTERVAL_MS = 30_000;
	const UNVIEWED_INTERVAL_MS = 15_000;

	const stream = new FeedStream();
	const cl = new CommandLineState();

	// Shared with the management views, which reload them after changes.
	const sources = $derived(metadata.sources);
	const tags = $derived(metadata.tags);
	let unviewedTotal: number | null = $state(null);
	let now = $state(Date.now());
	let note = $state('');
	let noteTimer: ReturnType<typeof setTimeout> | undefined;

	let expandedId: string | null = $state(null);
	let sheetOpen = $state(false);
	let searchFocused = $state(false);
	let mobile = $state(false);

	let list: VirtualList<Item> | undefined = $state();
	let searchInput: HTMLInputElement | undefined = $state();

	const filter: Filter = $derived(filterFromParams(page.url.searchParams));
	let draft = $state('');

	const feed = $derived(stream.state);
	const items = $derived(feed.items);
	const cursor = $derived(feed.cursor);
	const expandedIndex = $derived(expandedId === null ? -1 : items.findIndex((it) => it.id === expandedId));
	const srcMeta = $derived(sourceDisplays(sources));
	const tagMeta = $derived(tagDisplays(tags));
	const times = $derived(fetchTimes(sources, now));
	const localUnviewed = $derived(items.reduce((n, it) => n + (it.viewed ? 0 : 1), 0));
	const mode: Mode = $derived(
		cl.open ? 'command' : sheetOpen ? 'sheet' : searchFocused ? 'search' : 'normal'
	);

	// ── Filter ⇄ URL ──────────────────────────────────────────

	let connected: Filter | null = null;
	$effect(() => {
		const f = filter;
		untrack(() => {
			draft = f.q;
			if (connected && sameFilter(connected, f)) return;
			connected = f;
			// ":feed" and the view tabs come back to this filter.
			metadata.feedSearch = filterQuery(f);
			stream.connect(f);
			void loadUnviewed();
		});
	});

	function setFilter(f: Filter) {
		if (sameFilter(f, filter)) return;
		void goto(page.url.pathname + filterQuery(f), { keepFocus: true, noScroll: true });
	}

	// ── Metadata and counts ───────────────────────────────────

	function loadMetadata() {
		// Errors are left to the status bar's connection state.
		return metadata.reload();
	}

	async function loadUnviewed() {
		try {
			unviewedTotal = (await api.searchItems({ ...filter, unviewed: true }, 1)).total;
		} catch {
			unviewedTotal = null;
		}
	}

	function flash(msg: string) {
		note = msg;
		clearTimeout(noteTimer);
		noteTimer = setTimeout(() => (note = ''), 4000);
	}

	// ── View tracking ─────────────────────────────────────────

	const tracker = new ViewTracker(
		async (ids) => {
			await api.markViewed(ids);
			void loadUnviewed();
		},
		(ids) => api.beaconViewed(ids),
		(ids) => {
			stream.set(markViewed(stream.state, ids));
			if (unviewedTotal !== null) unviewedTotal = Math.max(0, unviewedTotal - ids.size);
		}
	);

	function onrange(first: number, last: number) {
		// Only rows someone can actually see count: not before the snapshot
		// has arrived, and not while the tab is in the background.
		if (!feed.complete || document.visibilityState !== 'visible') return;
		const ids: string[] = [];
		for (let i = first; i <= last; i++) {
			const it = items[i];
			if (it && !it.viewed) ids.push(it.id);
		}
		tracker.see(ids);
	}

	// ── Actions ───────────────────────────────────────────────

	function select(i: number) {
		stream.set(moveCursor(stream.state, i));
		list?.ensureVisible(stream.state.cursor);
	}

	function toggleExpand() {
		const it = items[cursor];
		if (!it) return;
		expandedId = expandedId === it.id ? null : it.id;
		queueMicrotask(() => list?.ensureVisible(cursor));
	}

	function onRowSelect(i: number) {
		const wasSelected = i === cursor;
		select(i);
		// Tap a row to select and expand it (mobile); click a selected row
		// to toggle it (both).
		if (wasSelected || mobile) {
			const it = items[i];
			if (it) expandedId = wasSelected && expandedId === it.id ? null : it.id;
			queueMicrotask(() => list?.ensureVisible(i));
		}
	}

	function openLink() {
		const link = safeLink(items[cursor]?.link);
		if (link) window.open(link, '_blank', 'noopener,noreferrer');
	}

	async function refresh(sourceId?: string) {
		try {
			await api.refresh(sourceId);
			flash(sourceId ? 'refreshing source' : 'refreshing all sources');
			setTimeout(() => void loadMetadata(), 3000);
		} catch (e) {
			flash('refresh failed: ' + (e instanceof Error ? e.message : String(e)));
		}
	}

	function applySearch() {
		setFilter({ ...filter, q: draft.trim() });
		searchInput?.blur();
		list?.focus();
	}

	function clearSearch() {
		draft = '';
		setFilter({ ...filter, q: '' });
		searchInput?.blur();
		list?.focus();
	}

	function run(a: Action) {
		switch (a.type) {
			case 'move':
				return select(cursor + a.by);
			case 'halfPage':
				return select(cursor + a.dir * Math.max(1, Math.floor((list?.pageRows() ?? 2) / 2)));
			case 'top':
				return select(0);
			case 'bottom':
				return select(items.length - 1);
			case 'focusSearch':
				searchInput?.focus();
				searchInput?.select();
				return;
			case 'applySearch':
				return applySearch();
			case 'clearSearch':
				return clearSearch();
			case 'cycleSource':
				return setFilter({ ...filter, source: cycleID(ids(sources), filter.source) });
			case 'cycleTag':
				return setFilter({ ...filter, tag: cycleID(ids(tags), filter.tag) });
			case 'toggleSort':
				return setFilter({ ...filter, sort: nextSort(filter.sort) });
			case 'refreshAll':
				return void refresh();
			case 'refreshSource': {
				const id = items[cursor]?.source_id;
				return id ? void refresh(id) : undefined;
			}
			case 'open':
				return openLink();
			case 'toggleExpand':
				return toggleExpand();
			case 'close':
				if (sheetOpen) sheetOpen = false;
				else expandedId = null;
				return;
			case 'openCommand':
				return cl.start();
			case 'runCommand':
				return cl.run();
			case 'cancelCommand':
				cl.cancel();
				list?.focus();
				return;
		}
	}

	function ids(xs: { id?: string }[]): string[] {
		return xs.map((x) => x.id).filter((id): id is string => !!id);
	}

	function onkeydown(e: KeyboardEvent) {
		const target = e.target as HTMLElement | null;
		// Other controls (sheet selects, buttons) keep their own keys.
		if (
			mode === 'normal' &&
			target &&
			target !== searchInput &&
			target.closest('input, select, textarea, button, a')
		) {
			return;
		}
		const a = keyAction(mode, e);
		if (!a) return;
		e.preventDefault();
		run(a);
	}

	// ── Lifecycle ─────────────────────────────────────────────

	onMount(() => {
		const mq = window.matchMedia('(max-width: 719.98px)');
		mobile = mq.matches;
		const onMq = () => (mobile = mq.matches);
		mq.addEventListener('change', onMq);

		void loadMetadata();
		tracker.start();
		const metaTimer = setInterval(() => void loadMetadata(), METADATA_INTERVAL_MS);
		const unviewedTimer = setInterval(() => void loadUnviewed(), UNVIEWED_INTERVAL_MS);
		const clock = setInterval(() => (now = Date.now()), 1000);

		// When the page is hidden or goes away, send what is pending with a
		// beacon: unlike fetch, it is delivered even while the page unloads.
		// Navigating away fires visibilitychange before pagehide, so both
		// must use it. pagehide also covers the back/forward cache.
		const onPageHide = () => tracker.flushBeacon();
		const onVisibility = () => {
			if (document.visibilityState === 'hidden') tracker.flushBeacon();
			// Rows that arrived while the tab was hidden are seen now.
			else list?.reportRange();
		};
		window.addEventListener('pagehide', onPageHide);
		document.addEventListener('visibilitychange', onVisibility);

		list?.focus();

		return () => {
			mq.removeEventListener('change', onMq);
			window.removeEventListener('pagehide', onPageHide);
			document.removeEventListener('visibilitychange', onVisibility);
			clearInterval(metaTimer);
			clearInterval(unviewedTimer);
			clearInterval(clock);
			tracker.stop();
			tracker.flushBeacon();
			stream.close();
		};
	});
</script>

<svelte:window {onkeydown} />

<div class="app">
	<FilterBar
		{filter}
		{sources}
		{tags}
		bind:draft
		bind:input={searchInput}
		onsearchfocus={() => (searchFocused = true)}
		onsearchblur={() => (searchFocused = false)}
		oncyclesource={() => run({ type: 'cycleSource' })}
		oncycletag={() => run({ type: 'cycleTag' })}
		ontogglesort={() => run({ type: 'toggleSort' })}
		ontoggleunviewed={() => setFilter({ ...filter, unviewed: !filter.unviewed })}
		onopensheet={() => (sheetOpen = true)}
		onrefresh={() => void refresh()}
	/>

	<VirtualList
		bind:this={list}
		class="feed"
		{items}
		key={(it) => it.id}
		expanded={expandedIndex}
		{onrange}
		role="grid"
		aria-label="feed"
		aria-rowcount={items.length}
		aria-activedescendant={items[cursor] ? 'row-' + items[cursor].id : undefined}
		tabindex={0}
		data-testid="feed"
	>
		{#snippet row(item, i)}
			<FeedRow
				{item}
				index={i}
				selected={i === cursor}
				expanded={i === expandedIndex}
				source={item.source_id ? srcMeta.get(item.source_id) : undefined}
				tags={tagMeta}
				onselect={onRowSelect}
			/>
		{/snippet}
		{#snippet detail(item)}
			<ItemDetail {item} source={item.source_id ? srcMeta.get(item.source_id) : undefined} />
		{/snippet}
		{#snippet empty()}
			<div class="empty">
				{#if !feed.complete}
					loading…
				{:else}
					no items — waiting for feeds...
				{/if}
			</div>
		{/snippet}
	</VirtualList>

	<StatusBar
		status={stream.status}
		unviewed={unviewedTotal ?? localUnviewed}
		{times}
		{now}
		{note}
		shown={items.length}
	/>
</div>

<CommandLine {cl} />

{#if sheetOpen}
	<FilterSheet {filter} {sources} {tags} onchange={setFilter} onclose={() => (sheetOpen = false)} />
{/if}

<style>
	.app {
		display: grid;
		grid-template-rows: auto minmax(0, 1fr) auto;
		height: 100vh;
		height: 100dvh;
		padding-left: env(safe-area-inset-left);
		padding-right: env(safe-area-inset-right);
	}
	.app :global(.feed) {
		height: 100%;
	}
	.empty {
		display: grid;
		place-items: center;
		height: 100%;
		color: var(--dim);
	}
</style>
