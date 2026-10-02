<!--
	The frame of the sources, tags and rules pages: page tabs, a list with
	the feed's row style, the add/edit or delete panel at the bottom, and a
	toolbar that doubles as the key legend. One global keydown listener
	drives it through manageKeyAction(); the page gets the actions that are
	about its items (add, edit, delete, toggle, refresh, cancel, confirm).

	The lists are short (tens of rows), so unlike the feed they are not
	virtual.
-->
<script lang="ts" generics="T">
	import type { Snippet } from 'svelte';
	import { onMount, tick } from 'svelte';
	import { PAGES, type Page } from './command';
	import CommandLine from './CommandLine.svelte';
	import * as api from './api';
	import { CommandLine as CommandLineState, execute, goPage, offFeedHost, pageHref } from './commandline.svelte';
	import Help from './Help.svelte';
	import { manageSections } from './help';
	import { manageKeyAction, type ManageAction, type ManageMode, type Tool } from './keymap';
	import { metadata } from './metadata.svelte';

	interface Props {
		page: Exclude<Page, 'feed'>;
		items: T[];
		key: (item: T) => string;
		cursor: number;
		/** What the bottom panel shows, if anything. */
		panel: 'form' | 'confirm' | null;
		tools: Tool[];
		/** A transient message; errors start with "error". */
		note: string;
		/** Counts shown in the tabs. */
		counts?: Partial<Record<Page, number>>;
		loaded: boolean;
		onaction: (a: ManageAction) => void;
		row: Snippet<[T, number]>;
		panelContent?: Snippet;
		empty: Snippet;
	}

	let {
		page,
		items,
		key,
		cursor = $bindable(),
		panel,
		tools,
		note,
		counts = {},
		loaded,
		onaction,
		row,
		panelContent,
		empty
	}: Props = $props();

	const cl = new CommandLineState();
	let helpOpen = $state(false);
	/** A message from a command (":refresh"), shown when the page has none of its own. */
	let cmdNote = $state('');
	let cmdNoteTimer: ReturnType<typeof setTimeout> | undefined;
	const shownNote = $derived(note || cmdNote);
	let list: HTMLDivElement | undefined = $state();
	let panelEl: HTMLElement | undefined = $state();

	const mode: ManageMode = $derived(cl.open ? 'command' : helpOpen ? 'help' : (panel ?? 'normal'));
	const helpSections = $derived(
		manageSections(tools.map((t) => t.type))
	);

	function flashCmd(msg: string) {
		cmdNote = msg;
		clearTimeout(cmdNoteTimer);
		cmdNoteTimer = setTimeout(() => (cmdNote = ''), 4000);
	}

	async function refreshCmd(id?: string) {
		try {
			await api.refresh(id);
			flashCmd(id ? 'refreshing source' : 'refreshing all sources');
		} catch (e) {
			flashCmd('error: refresh failed: ' + (e instanceof Error ? e.message : String(e)));
		}
	}

	const host = { ...offFeedHost(flashCmd, (id) => void refreshCmd(id)), help: () => (helpOpen = true) };

	function closeHelp() {
		helpOpen = false;
		list?.focus({ preventScroll: true });
	}
	const toolTypes = $derived(new Set<ManageAction['type']>(tools.map((t) => t.type)));
	const selected = $derived(items[cursor]);

	// Keep the cursor on the list when it shrinks.
	$effect(() => {
		if (cursor >= items.length && items.length > 0) cursor = items.length - 1;
		if (cursor < 0) cursor = 0;
	});

	// Back to the list when the panel closes; into a confirmation when it
	// opens (so y/n work without a click). Forms focus their first field.
	let lastPanel: typeof panel = null;
	$effect(() => {
		const p = panel;
		if (p === lastPanel) return;
		lastPanel = p;
		void tick().then(() => {
			if (p === null) list?.focus({ preventScroll: true });
			else if (p === 'confirm') panelEl?.focus({ preventScroll: true });
		});
	});

	function select(i: number) {
		if (items.length === 0) return;
		cursor = Math.max(0, Math.min(i, items.length - 1));
		void tick().then(() => {
			const k = items[cursor];
			if (k === undefined) return;
			document.getElementById(rowId(k))?.scrollIntoView({ block: 'nearest' });
		});
	}

	function rowId(item: T): string {
		return `${page}-row-${key(item)}`;
	}

	function pageRows(): number {
		const rowH = list?.firstElementChild?.getBoundingClientRect().height || 20;
		return Math.max(1, Math.floor((list?.clientHeight ?? rowH * 2) / rowH));
	}

	function run(a: ManageAction) {
		switch (a.type) {
			case 'move':
				return select(cursor + a.by);
			case 'halfPage':
				return select(cursor + a.dir * Math.max(1, Math.floor(pageRows() / 2)));
			case 'top':
				return select(0);
			case 'bottom':
				return select(items.length - 1);
			case 'openCommand':
				return cl.start();
			case 'runCommand': {
				const cmd = cl.run({ sources: metadata.sources, tags: metadata.tags, views: metadata.views });
				if (!cmd) return;
				list?.focus({ preventScroll: true });
				return execute(cmd, host);
			}
			case 'cancelCommand':
				cl.cancel();
				list?.focus({ preventScroll: true });
				return;
			case 'completeCommand':
				return cl.complete({ sources: metadata.sources, tags: metadata.tags, views: metadata.views });
			case 'historyPrev':
				return cl.historyPrev();
			case 'historyNext':
				return cl.historyNext();
			case 'openHelp':
				helpOpen = true;
				return;
			case 'closeHelp':
				return closeHelp();
			case 'feed':
				return void goPage('feed');
			case 'cancel':
			case 'confirm':
				return onaction(a);
			default:
				if (!toolTypes.has(a.type)) return;
				if (a.type !== 'add' && !selected) return;
				return onaction(a);
		}
	}

	function onkeydown(e: KeyboardEvent) {
		const target = e.target as HTMLElement | null;
		// Buttons and links keep Enter and Space: a focused "cancel" must
		// never confirm a delete.
		if (mode !== 'command' && (e.key === 'Enter' || e.key === ' ') && target?.closest('button, a')) return;
		// Form controls outside a panel (none today) keep their keys.
		if (mode === 'normal' && target?.closest('input, select, textarea')) return;
		const a = manageKeyAction(mode, e);
		if (!a) return;
		// Movement and tools only apply with no panel open.
		if (mode !== 'normal' && mode !== 'command' && mode !== 'help' && a.type !== 'cancel' && a.type !== 'confirm') {
			return;
		}
		e.preventDefault();
		run(a);
	}

	// A click or tap only selects: the toolbar and keys act on the
	// selection, so a stray second tap never opens a form.
	function onRowClick(i: number) {
		select(i);
	}

	function onTool(t: Tool) {
		run({ type: t.type });
	}

	onMount(() => {
		list?.focus({ preventScroll: true });
	});
</script>

<svelte:window {onkeydown} />

<div class="manage">
	<nav class="tabs" aria-label="pages">
		{#each PAGES as v (v)}
			<a
				href={pageHref(v)}
				class:current={v === page}
				aria-current={v === page ? 'page' : undefined}
				data-testid="nav-{v}"
				>{v}{#if counts[v] !== undefined}<span class="n">{' ' + counts[v]}</span>{/if}</a
			>
		{/each}
		<span class="hint">: command · q feed</span>
	</nav>

	<div
		bind:this={list}
		class="list"
		role="grid"
		aria-label={page}
		aria-rowcount={items.length}
		aria-activedescendant={selected ? rowId(selected) : undefined}
		tabindex={0}
		data-testid="{page}-list"
	>
		{#if items.length === 0}
			<div class="empty">
				{#if !loaded}loading…{:else}{@render empty()}{/if}
			</div>
		{:else}
			{#each items as item, i (key(item))}
				<!-- Clicks are a convenience; the grid is driven from the keyboard. -->
				<!-- svelte-ignore a11y_click_events_have_key_events -->
				<div
					class="mrow"
					class:selected={i === cursor}
					role="row"
					tabindex={-1}
					id={rowId(item)}
					aria-selected={i === cursor}
					data-id={key(item)}
					onclick={() => onRowClick(i)}
				>
					<div class="mcells" role="gridcell">{@render row(item, i)}</div>
				</div>
			{/each}
		{/if}
	</div>

	{#if panel && panelContent}
		<section
			class="panel"
			class:confirm={panel === 'confirm'}
			bind:this={panelEl}
			tabindex="-1"
			aria-label={panel === 'confirm' ? 'confirm delete' : 'edit'}
			data-testid="panel"
		>
			{@render panelContent()}
		</section>
	{/if}

	{#if shownNote}
		<div class="note" class:error={shownNote.startsWith('error')} role="status" data-testid="note">{shownNote}</div>
	{/if}

	<div class="toolbar" data-testid="toolbar">
		{#each tools as t (t.type)}
			<button
				type="button"
				disabled={panel !== null || (t.type !== 'add' && !selected)}
				onclick={() => onTool(t)}
				data-testid="tool-{t.type}"><span class="k">{t.key}</span> {t.label}</button
			>
		{/each}
		<button type="button" class="help" onclick={() => (helpOpen = true)} data-testid="open-help"
			><span class="k">?</span> help</button
		>
	</div>
</div>

<CommandLine {cl} />

{#if helpOpen}
	<Help title="Keys: {page}" sections={helpSections} onclose={closeHelp} />
{/if}

<style>
	.manage {
		display: grid;
		grid-template-rows: auto minmax(0, 1fr) auto auto auto;
		height: 100vh;
		height: 100dvh;
		padding-left: env(safe-area-inset-left);
		padding-right: env(safe-area-inset-right);
	}

	.tabs {
		display: flex;
		align-items: center;
		gap: 2ch;
		height: var(--bar-h);
		padding: 0 1ch;
		background: var(--bar-filter);
		white-space: nowrap;
		overflow: hidden;
	}
	.tabs a {
		color: var(--dim);
		text-decoration: none;
	}
	.tabs a.current {
		color: var(--fg-strong);
	}
	.tabs a.current::before {
		content: '[';
		color: var(--dim);
	}
	.tabs a.current::after {
		content: ']';
		color: var(--dim);
	}
	.n {
		color: var(--dim);
	}
	.hint {
		margin-left: auto;
		color: var(--dim);
	}

	.list {
		overflow-y: auto;
		overflow-x: hidden;
		overscroll-behavior: contain;
		outline: none;
		min-height: 0;
	}
	.empty {
		display: grid;
		place-items: center;
		height: 100%;
		padding: 1ch;
		color: var(--dim);
		text-align: center;
	}
	.mrow {
		height: var(--row-h);
		overflow: hidden;
		cursor: default;
		user-select: none;
	}
	.mrow.selected {
		background: var(--sel);
	}
	@media (hover: hover) {
		.mrow:not(.selected):hover {
			background: #262628;
		}
	}
	.mcells {
		display: flex;
		align-items: center;
		gap: 1ch;
		height: 100%;
		padding: 0 1ch;
		white-space: nowrap;
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

	@media (max-width: 719.98px) {
		.tabs {
			gap: 0;
			padding: env(safe-area-inset-top) 0 0;
			height: calc(var(--bar-h) + env(safe-area-inset-top));
		}
		.tabs a {
			flex: 1;
			display: flex;
			align-items: center;
			justify-content: center;
			height: 100%;
			white-space: pre;
		}
		.tabs a.current {
			background: var(--sel);
		}
		.tabs a.current::before,
		.tabs a.current::after {
			content: none;
		}
		.hint {
			display: none;
		}
		.mcells {
			flex-wrap: wrap;
			align-content: center;
			row-gap: 0;
			padding: 0 8px;
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
	}
</style>
