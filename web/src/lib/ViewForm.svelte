<!--
	Add or edit a saved view: a name, the filter as query text and a favorite
	checkbox. The query is the "/" bar's syntax (query.ts), parsed with the
	same parser, so the same names and errors apply; the rows of the views
	page show the filter in this form too.
-->
<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { hasErrors, validateView, type ViewForm } from './forms';
	import type { Assessor, Source, Tag } from './types';

	interface Props {
		initial: ViewForm;
		editing: boolean;
		sources: Source[];
		tags: Tag[];
		assessors?: Assessor[];
		onsave: (f: ViewForm) => Promise<void>;
		oncancel: () => void;
	}

	let { initial, editing, sources, tags, assessors = [], onsave, oncancel }: Props = $props();

	let f: ViewForm = $state(untrack(() => ({ ...initial })));
	let submitted = $state(false);
	let saving = $state(false);
	let serverError = $state('');
	let form: HTMLFormElement | undefined = $state();

	const errors = $derived(submitted ? validateView(f, sources, tags, assessors) : {});

	onMount(() => form?.querySelector<HTMLInputElement>('input')?.focus());

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (saving) return;
		submitted = true;
		if (hasErrors(validateView(f, sources, tags, assessors))) {
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

<form class="mform" bind:this={form} onsubmit={submit} novalidate data-testid="view-form">
	<div class="title">{editing ? 'edit view' : 'add view'}</div>
	<div class="fields">
		<div class="field">
			<label
				><span class="k">name</span><input
					type="text"
					bind:value={f.name}
					maxlength="64"
					autocomplete="off"
					autocapitalize="off"
					aria-invalid={!!errors.name}
					aria-describedby="view-name-err"
					data-testid="view-name"
				/></label
			>
			{#if errors.name}<span class="ferr" id="view-name-err">{errors.name}</span>{/if}
		</div>
		<div class="field grow">
			<label
				><span class="k">filter</span><input
					type="text"
					bind:value={f.query}
					maxlength="500"
					placeholder={'words  tag:<name>  src:<name>  is:unviewed  score:<assessor>>=0.7  sort:oldest'}
					autocomplete="off"
					autocapitalize="off"
					spellcheck="false"
					aria-invalid={!!errors.query}
					aria-describedby="view-query-err"
					data-testid="view-query"
				/></label
			>
			{#if errors.query}<span class="ferr" id="view-query-err" data-testid="view-query-error">{errors.query}</span>{/if}
		</div>
		<div class="field">
			<label
				><span class="k">favorite</span><input
					type="checkbox"
					bind:checked={f.favorite}
					data-testid="view-favorite"
				/></label
			>
		</div>
	</div>
	{#if serverError}<div class="server-error" role="alert">{serverError}</div>{/if}
	<div class="actions">
		<button type="submit" class="primary" disabled={saving} data-testid="save">save</button>
		<button type="button" onclick={oncancel}>cancel</button>
		<span class="hint">Enter saves · Esc cancels</span>
	</div>
</form>
