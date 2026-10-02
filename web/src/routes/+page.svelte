<!--
	The feed: filter bar, virtual table and status bar. The filter lives in
	the URL as separate parameters (?q=&source=&tag=&sort=&unviewed=1, IDs for
	source and tag), so views can be bookmarked, survive renames, and the back
	button works. The query bar shows it as text (tag:rust src:"Hacker News"
	...) and parses what you type back into those parameters (query.ts).
-->
<script lang="ts">
	import { onMount, tick, untrack } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import * as api from '$lib/api';
	import FeedRow from '$lib/FeedRow.svelte';
	import FilterBar from '$lib/FilterBar.svelte';
	import FilterSheet from '$lib/FilterSheet.svelte';
	import Help from '$lib/Help.svelte';
	import ItemDetail from '$lib/ItemDetail.svelte';
	import PickerView from '$lib/Picker.svelte';
	import StatusBar from '$lib/StatusBar.svelte';
	import VirtualList from '$lib/VirtualList.svelte';
	import { cycleID, filterFromParams, filterQuery, nextSort, sameFilter } from '$lib/filter';
	import CommandLine from '$lib/CommandLine.svelte';
	import { CommandLine as CommandLineState, execute, type CommandHost } from '$lib/commandline.svelte';
	import { feedSections } from '$lib/help';
	import { highlightTerms } from '$lib/highlight';
	import { keyAction, type Action, type Mode } from '$lib/keymap';
	import { fetchTimes, sourceDisplays, tagDisplays } from '$lib/meta';
	import { metadata } from '$lib/metadata.svelte';
	import { Picker } from '$lib/picker.svelte';
	import { prefs } from '$lib/prefs.svelte';
	import { treeOrder } from '$lib/tagtree';
	import { applyCompletion, complete, format, parse, type Candidate } from '$lib/query';
	import { markViewed, moveCursor, setFollow } from '$lib/reducer';
	import { oneLine, safeLink } from '$lib/sanitize';
	import { FeedStream } from '$lib/stream.svelte';
	import type { Filter, Item } from '$lib/types';
	import { ViewTracker } from '$lib/viewed';

	const METADATA_INTERVAL_MS = 30_000;
	const UNVIEWED_INTERVAL_MS = 15_000;
	/** Fetch the next page when the view is this close to the end of the list. */
	const LOAD_AHEAD_ROWS = 10;

	const stream = new FeedStream();
	const cl = new CommandLineState();
	const picker = new Picker();
	const helpSections = feedSections();

	// Shared with the management views, which reload them after changes.
	const sources = $derived(metadata.sources);
	const tags = $derived(metadata.tags);
	// Tree order, each tag once: the order of the t key and the T picker.
	const treeTags = $derived(treeOrder(tags));
	let unviewedTotal: number | null = $state(null);
	let now = $state(Date.now());
	let note = $state('');
	let noteTimer: ReturnType<typeof setTimeout> | undefined;

	let expandedId: string | null = $state(null);
	let sheetOpen = $state(false);
	let helpOpen = $state(false);
	let searchFocused = $state(false);
	let mobile = $state(false);

	let list: VirtualList<Item> | undefined = $state();
	let searchInput: HTMLInputElement | undefined = $state();

	const filter: Filter = $derived(filterFromParams(page.url.searchParams));
	/** The query bar's text. */
	let draft = $state('');
	/** The filter as query text, which the bar shows when it is not being edited. */
	const canonical = $derived(format(filter, sources, tags));
	/** The text last applied, so the bar keeps it while the URL catches up. */
	let applied: string | null = null;
	const parsed = $derived(parse(draft, sources, tags));
	let caret = $state(0);
	let active = $state(-1);
	let submitError = $state('');
	const completion = $derived(searchFocused ? complete(draft, caret, sources, tags) : null);
	const suggestions = $derived(completion?.candidates ?? []);
	// Live errors show only when there is nothing to complete: "tag:ru" is
	// not wrong, it is unfinished.
	const queryError = $derived(
		submitError || (searchFocused && draft.trim() && suggestions.length === 0 ? (parsed.errors[0]?.message ?? '') : '')
	);
	const terms = $derived(highlightTerms(filter.q));

	const feed = $derived(stream.state);
	const items = $derived(feed.items);
	const cursor = $derived(feed.cursor);
	const expandedIndex = $derived(expandedId === null ? -1 : items.findIndex((it) => it.id === expandedId));
	const srcMeta = $derived(sourceDisplays(sources));
	const tagMeta = $derived(tagDisplays(tags));
	const times = $derived(fetchTimes(sources, now));
	const localUnviewed = $derived(items.reduce((n, it) => n + (it.viewed ? 0 : 1), 0));
	const mode: Mode = $derived(
		cl.open
			? 'command'
			: helpOpen
				? 'help'
				: picker.open
					? 'picker'
					: sheetOpen
						? 'sheet'
						: searchFocused
							? 'search'
							: 'normal'
	);
	const followState = $derived(filter.sort === 'newest' ? (feed.follow ? 'on' : 'off') : 'na');

	// ── Filter ⇄ URL ──────────────────────────────────────────

	let connected: Filter | null = null;
	$effect(() => {
		const f = filter;
		untrack(() => {
			if (connected && sameFilter(connected, f)) return;
			connected = f;
			// ":feed" and the view tabs come back to this filter.
			metadata.feedSearch = filterQuery(f);
			stream.connect(f);
			void loadUnviewed();
		});
	});

	// The bar shows the filter as text unless it is being edited: after the
	// URL changes (keys, chips, back button) and when the names load.
	$effect(() => {
		const text = canonical;
		untrack(() => {
			if (!searchFocused) draft = text;
		});
	});

	// Suggestions start unhighlighted whenever the text or caret changes.
	$effect(() => {
		void draft;
		void caret;
		untrack(() => (active = -1));
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
		loadOlder(last);
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

	function leaveSearch() {
		submitError = '';
		searchInput?.blur();
		list?.focus();
	}

	/** Applies the typed query, or says what is wrong with it and stays in the bar. */
	function applySearch() {
		const pick = suggestions[active];
		if (pick) return accept(pick);
		const r = parsed;
		if (r.errors.length) {
			submitError = r.errors[0]!.message;
			return;
		}
		draft = applied = format(r.filter, sources, tags);
		setFilter(r.filter);
		leaveSearch();
	}

	/** Esc: drops the search text, keeps the operators (they have their own keys and chips). */
	function clearSearch() {
		const f = { ...filter, q: '' };
		draft = applied = format(f, sources, tags);
		setFilter(f);
		leaveSearch();
	}

	function onsearchblur() {
		searchFocused = false;
		// Abandoned edits are dropped; what was just applied stays until the URL has it.
		if (draft !== applied) draft = canonical;
		applied = null;
	}

	/** Puts a suggested name into the text where the caret is. */
	function accept(c: Candidate) {
		if (!completion) return;
		const r = applyCompletion(draft, completion, c);
		draft = r.text;
		submitError = '';
		void tick().then(() => {
			searchInput?.focus();
			searchInput?.setSelectionRange(r.caret, r.caret);
			caret = r.caret;
		});
	}

	function follow() {
		// Follow is a newest-first mode: from oldest first, F goes there too.
		if (filter.sort !== 'newest') setFilter({ ...filter, sort: 'newest' });
		select(0);
		list?.scrollToTop();
	}

	function onattop(atTop: boolean) {
		stream.set(setFollow(stream.state, atTop));
	}

	function loadOlder(last: number) {
		if (last >= items.length - 1 - LOAD_AHEAD_ROWS) void stream.loadOlder();
	}

	function openPicker(kind: 'source' | 'tag') {
		const entries =
			kind === 'source'
				? sources.map((x) => ({ x, depth: 0 }))
				: treeTags.map((r) => ({ x: r.tag, depth: r.depth }));
		picker.start(kind, [
			{ id: '', label: 'all' },
			...entries.flatMap(({ x, depth }) => (x.id ? [{ id: x.id, label: oneLine(x.name), color: x.color, depth }] : []))
		]);
	}

	function closePicker() {
		picker.close();
		list?.focus();
	}

	function pick(id: string) {
		const kind = picker.kind;
		closePicker();
		if (kind) setFilter({ ...filter, [kind]: id });
	}

	function closeHelp() {
		helpOpen = false;
		list?.focus();
	}

	const host: CommandHost = {
		help: () => (helpOpen = true),
		flash,
		refresh: (id) => void refresh(id),
		filter: () => filter,
		setFilter,
		follow
	};

	/** Runs an action; false means the key was not used, so the browser keeps it. */
	function run(a: Action): boolean | void {
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
			case 'completeQuery': {
				const c = suggestions[Math.max(active, 0)];
				return c ? accept(c) : false;
			}
			case 'suggestMove': {
				const n = suggestions.length;
				if (n === 0) return false;
				// -1 (none highlighted) is one more stop in the cycle.
				active = ((((active + 1 + a.by) % (n + 1)) + (n + 1)) % (n + 1)) - 1;
				return;
			}
			case 'cycleSource':
				return setFilter({ ...filter, source: cycleID(ids(sources), filter.source) });
			case 'cycleTag':
				return setFilter({ ...filter, tag: cycleID(ids(treeTags.map((r) => r.tag)), filter.tag) });
			case 'pickSource':
				return openPicker('source');
			case 'pickTag':
				return openPicker('tag');
			case 'pickerMove':
				return picker.move(a.by);
			case 'pickerSelect': {
				const o = picker.selected();
				return o ? pick(o.id) : undefined;
			}
			case 'pickerCancel':
				return closePicker();
			case 'toggleSort':
				return setFilter({ ...filter, sort: nextSort(filter.sort) });
			case 'toggleTime':
				prefs.toggleTime();
				return flash('times: ' + prefs.timeMode);
			case 'follow':
				return follow();
			case 'openHelp':
				helpOpen = true;
				return;
			case 'closeHelp':
				return closeHelp();
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
			case 'runCommand': {
				const cmd = cl.run({ sources, tags });
				if (!cmd) return;
				list?.focus();
				return execute(cmd, host);
			}
			case 'cancelCommand':
				cl.cancel();
				list?.focus();
				return;
			case 'completeCommand':
				return cl.complete({ sources, tags });
			case 'historyPrev':
				return cl.historyPrev();
			case 'historyNext':
				return cl.historyNext();
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
		if (run(a) === false) return;
		e.preventDefault();
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
		{suggestions}
		{active}
		error={queryError}
		oncaret={(pos) => {
			caret = pos;
			submitError = '';
		}}
		onpick={accept}
		onhelp={() => (helpOpen = true)}
		onsearchfocus={() => (searchFocused = true)}
		{onsearchblur}
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
		{onattop}
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
				{terms}
				timeMode={prefs.timeMode}
				{now}
			/>
		{/snippet}
		{#snippet detail(item)}
			<ItemDetail {item} source={item.source_id ? srcMeta.get(item.source_id) : undefined} {terms} />
		{/snippet}
		{#snippet footer()}
			<span data-testid="older">
				{#if feed.loading}
					loading…
				{:else if feed.end}
					— end —
				{:else if stream.olderError}
					couldn't load older items: {stream.olderError}
				{/if}
			</span>
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
		follow={followState}
		pending={feed.pending}
		onfollow={follow}
		onhelp={() => (helpOpen = true)}
	/>
</div>

<CommandLine {cl} />

{#if helpOpen}
	<Help title="Keys: feed" sections={helpSections} onclose={closeHelp} />
{/if}

{#if picker.open}
	<PickerView {picker} onpick={pick} onclose={closePicker} />
{/if}

{#if sheetOpen}
	<FilterSheet
		{filter}
		{sources}
		{tags}
		timeMode={prefs.timeMode}
		onchange={setFilter}
		ontime={(m) => prefs.setTime(m)}
		onclose={() => (sheetOpen = false)}
	/>
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
