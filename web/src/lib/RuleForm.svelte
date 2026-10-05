<!--
	Add or edit a tag rule, with a live preview: after a short pause the
	pattern is dry-run on the daemon (TestTagRule), which matches with Go
	RE2 like the tagger. Patterns are never evaluated in the browser.
-->
<script lang="ts">
	import { onDestroy, onMount, untrack } from 'svelte';
	import * as api from './api';
	import { formatDate } from './format';
	import { hasErrors, patternError, validateRule, type RuleForm } from './forms';
	import { LatestRequest } from './latest';
	import type { SourceDisplay } from './meta';
	import { oneLine, safeColor } from './sanitize';
	import type { Item, Source, Tag } from './types';

	interface Props {
		initial: RuleForm;
		editing: boolean;
		tags: Tag[];
		sources: Source[];
		sourceLabels: Map<string, SourceDisplay>;
		onsave: (f: RuleForm) => Promise<void>;
		oncancel: () => void;
	}

	let { initial, editing, tags, sources, sourceLabels, onsave, oncancel }: Props = $props();

	/** Matches shown in the preview. */
	const PREVIEW_LIMIT = 20;
	const PREVIEW_DELAY_MS = 250;

	let f: RuleForm = $state(untrack(() => ({ ...initial })));
	let submitted = $state(false);
	let saving = $state(false);
	let serverError = $state('');
	let form: HTMLFormElement | undefined = $state();

	type Preview =
		| { state: 'empty' }
		| { state: 'loading'; items: Item[]; scanned: number }
		| { state: 'ok'; items: Item[]; scanned: number }
		| { state: 'error'; error: string };
	let preview: Preview = $state({ state: 'empty' });

	const errors = $derived(submitted ? validateRule(f) : {});

	const tester = new LatestRequest(
		(q: { pattern: string; field: string; source_id: string }, signal) =>
			api.testRule({ ...q, limit: PREVIEW_LIMIT }, signal),
		PREVIEW_DELAY_MS,
		(o) => {
			if (o.ok) preview = { state: 'ok', items: o.value.items, scanned: o.value.scanned };
			else preview = { state: 'error', error: o.error instanceof Error ? o.error.message : String(o.error) };
		}
	);

	// Re-test whenever what the rule matches changes.
	$effect(() => {
		const q = { pattern: f.pattern, field: f.field, source_id: f.source_id };
		untrack(() => {
			const perr = patternError(q.pattern);
			if (perr) {
				tester.cancel();
				preview = q.pattern === '' ? { state: 'empty' } : { state: 'error', error: perr };
				return;
			}
			// Keep the last matches on screen while the next ones load.
			const prev = preview.state === 'ok' || preview.state === 'loading' ? preview : { items: [], scanned: 0 };
			preview = { state: 'loading', items: prev.items, scanned: prev.scanned };
			tester.schedule(q);
		});
	});

	onMount(() => form?.querySelector<HTMLInputElement>('[data-testid="rule-pattern"]')?.focus());
	onDestroy(() => tester.cancel());

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (saving) return;
		submitted = true;
		if (hasErrors(validateRule(f))) {
			queueMicrotask(() => form?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus());
			return;
		}
		saving = true;
		serverError = '';
		try {
			await onsave(f);
		} catch (err) {
			serverError = err instanceof Error ? err.message : String(err);
		} finally {
			saving = false;
		}
	}
</script>

<form class="mform" bind:this={form} onsubmit={submit} novalidate data-testid="rule-form">
	<div class="title">{editing ? 'edit rule (saves a new rule, then removes the old one)' : 'add rule'}</div>
	<div class="fields">
		<div class="field">
			<label
				><span class="k">tag</span><select
					bind:value={f.tag_id}
					aria-invalid={!!errors.tag_id}
					data-testid="rule-tag"
				>
					{#each tags as t (t.id)}
						<option value={t.id}>{oneLine(t.name)}</option>
					{/each}
				</select></label
			>
			{#if errors.tag_id}<span class="ferr">{errors.tag_id}</span>{/if}
		</div>
		<div class="field">
			<label
				><span class="k">field</span><select bind:value={f.field} data-testid="rule-field">
					<option value="both">both</option>
					<option value="title">title</option>
					<option value="description">description</option>
				</select></label
			>
		</div>
		<div class="field">
			<label
				><span class="k">source</span><select bind:value={f.source_id} data-testid="rule-source">
					<option value="">all</option>
					{#each sources as s (s.id)}
						<option value={s.id}>{oneLine(s.name)}</option>
					{/each}
				</select></label
			>
		</div>
		<div class="field">
			<label
				><span class="k">priority</span><input
					type="text"
					inputmode="numeric"
					bind:value={f.priority}
					size="4"
					autocomplete="off"
					aria-invalid={!!errors.priority}
					data-testid="rule-priority"
				/></label
			>
			{#if errors.priority}<span class="ferr">{errors.priority}</span>{/if}
		</div>
		<div class="field grow">
			<label
				><span class="k">pattern</span><input
					type="text"
					bind:value={f.pattern}
					placeholder="(?i)\bkernel\b"
					autocomplete="off"
					autocapitalize="off"
					autocorrect="off"
					spellcheck="false"
					aria-invalid={!!errors.pattern || preview.state === 'error'}
					aria-describedby="rule-preview"
					data-testid="rule-pattern"
				/></label
			>
			{#if errors.pattern}<span class="ferr">{errors.pattern}</span>{/if}
		</div>
	</div>

	<div class="preview" id="rule-preview" aria-live="polite" data-testid="rule-preview">
		{#if preview.state === 'empty'}
			<span class="dim">type a pattern (Go RE2 syntax, e.g. (?i) for case-insensitive) to see what it matches</span>
		{:else if preview.state === 'error'}
			<span class="bad" data-testid="rule-preview-error">{preview.error}</span>
		{:else}
			<div class="summary" class:loading={preview.state === 'loading'} data-testid="rule-preview-summary">
				{#if preview.state === 'loading' && preview.scanned === 0}
					testing…
				{:else}
					{preview.items.length}{preview.items.length >= PREVIEW_LIMIT ? '+' : ''}
					{preview.items.length === 1 ? 'match' : 'matches'} in the last {preview.scanned} items
					<span class="dim">· a new rule tags items fetched from now on; run <code>nyttig apply-tag-rules</code> to retag stored items</span>
				{/if}
			</div>
			{#each preview.items as it (it.id)}
				{@const src = it.source_id ? sourceLabels.get(it.source_id) : undefined}
				<div class="match" data-testid="rule-match">
					<span class="dim">{formatDate(it.published)}</span>
					{#if src?.label}<span class="dim"
							>[<span style:color={safeColor(src.color)}>{oneLine(src.label)}</span>]</span
						>{/if}
					<span class="t">{oneLine(it.title) || '(untitled)'}</span>
				</div>
			{/each}
		{/if}
	</div>

	{#if serverError}<div class="server-error" role="alert">{serverError}</div>{/if}
	<div class="actions">
		<button type="submit" class="primary" disabled={saving} data-testid="save">save</button>
		<button type="button" onclick={oncancel}>cancel</button>
		<span class="hint">Enter saves · Esc cancels</span>
	</div>
</form>

<style>
	.preview {
		border-left: 2px solid var(--sel);
		padding-left: 1ch;
		min-height: 20px;
	}
	.summary.loading {
		color: var(--dim);
	}
	.dim {
		color: var(--dim);
	}
	.bad {
		color: var(--error);
		overflow-wrap: anywhere;
	}
	.match {
		display: flex;
		gap: 1ch;
		white-space: nowrap;
		overflow: hidden;
	}
	.t {
		overflow: hidden;
		text-overflow: ellipsis;
	}
	input[data-testid='rule-pattern'] {
		font-family: var(--font);
	}
</style>
