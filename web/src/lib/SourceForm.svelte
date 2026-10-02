<!-- Add or edit a source, in the bottom panel of the sources view. -->
<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import ColorField from './ColorField.svelte';
	import { hasErrors, validateSource, type SourceForm } from './forms';

	interface Props {
		initial: SourceForm;
		editing: boolean;
		/** Saves the form; a thrown error is shown in the form. */
		onsave: (f: SourceForm) => Promise<void>;
		oncancel: () => void;
	}

	let { initial, editing, onsave, oncancel }: Props = $props();

	let f: SourceForm = $state(untrack(() => ({ ...initial })));
	let submitted = $state(false);
	let saving = $state(false);
	let serverError = $state('');
	let form: HTMLFormElement | undefined = $state();

	const errors = $derived(submitted ? validateSource(f) : {});

	onMount(() => form?.querySelector<HTMLInputElement>('input')?.focus());

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (saving) return;
		submitted = true;
		if (hasErrors(validateSource(f))) {
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

{#snippet err(name: keyof SourceForm)}
	{#if errors[name]}<span class="ferr" id="src-{name}-err">{errors[name]}</span>{/if}
{/snippet}

<form class="mform" bind:this={form} onsubmit={submit} novalidate data-testid="source-form">
	<div class="title">{editing ? 'edit source' : 'add source'}</div>
	<div class="fields">
		<div class="field">
			<label
				><span class="k">name</span><input
					type="text"
					bind:value={f.name}
					maxlength="200"
					autocomplete="off"
					aria-invalid={!!errors.name}
					aria-describedby="src-name-err"
					data-testid="source-name"
				/></label
			>
			{@render err('name')}
		</div>
		<div class="field grow">
			<label
				><span class="k">url</span><input
					type={f.type === 'bluesky' ? 'text' : 'url'}
					bind:value={f.url}
					maxlength="2048"
					placeholder={f.type === 'bluesky' ? 'alice.bsky.social' : 'https://example.com/feed.xml'}
					autocomplete="off"
					autocapitalize="off"
					spellcheck="false"
					inputmode="url"
					aria-invalid={!!errors.url}
					aria-describedby="src-url-err"
					data-testid="source-url"
				/></label
			>
			{@render err('url')}
			{#if f.type === 'bluesky'}
				<span class="hint"
					>a handle (alice.bsky.social), a DID, or a bsky.app profile URL; the name is optional</span
				>
			{/if}
		</div>
		<div class="field">
			<label
				><span class="k">type</span><select bind:value={f.type}>
					<option value="rss">rss</option>
					<option value="atom">atom</option>
					<option value="bluesky">bluesky</option>
				</select></label
			>
		</div>
		<div class="field">
			<label
				><span class="k">every</span><input
					type="text"
					bind:value={f.refresh}
					size="6"
					placeholder="1h"
					autocomplete="off"
					autocapitalize="off"
					spellcheck="false"
					aria-invalid={!!errors.refresh}
					aria-describedby="src-refresh-err"
					data-testid="source-refresh"
				/></label
			>
			{@render err('refresh')}
		</div>
		<div class="field">
			<label
				><span class="k">abbr</span><input
					type="text"
					bind:value={f.abbreviation}
					size="6"
					maxlength="16"
					autocomplete="off"
					aria-invalid={!!errors.abbreviation}
					aria-describedby="src-abbreviation-err"
					data-testid="source-abbreviation"
				/></label
			>
			{@render err('abbreviation')}
		</div>
		<div class="field">
			<div class="frow">
				<label for="src-color" class="k">color</label>
				<ColorField
					id="src-color"
					bind:value={f.color}
					invalid={!!errors.color}
					describedby="src-color-err"
				/>
			</div>
			{@render err('color')}
		</div>
		<div class="field">
			<label
				><span class="k">enabled</span><input
					type="checkbox"
					bind:checked={f.enabled}
					data-testid="source-enabled"
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
