<!-- Add or edit a tag (UpdateTag keeps its rules and item assignments). -->
<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import ColorField from './ColorField.svelte';
	import { hasErrors, validateTag, type TagForm } from './forms';

	interface Props {
		initial: TagForm;
		editing: boolean;
		onsave: (f: TagForm) => Promise<void>;
		oncancel: () => void;
	}

	let { initial, editing, onsave, oncancel }: Props = $props();

	let f: TagForm = $state(untrack(() => ({ ...initial })));
	let submitted = $state(false);
	let saving = $state(false);
	let serverError = $state('');
	let form: HTMLFormElement | undefined = $state();

	const errors = $derived(submitted ? validateTag(f) : {});

	onMount(() => form?.querySelector<HTMLInputElement>('input')?.focus());

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (saving) return;
		submitted = true;
		if (hasErrors(validateTag(f))) {
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

<form class="mform" bind:this={form} onsubmit={submit} novalidate data-testid="tag-form">
	<div class="title">{editing ? 'edit tag' : 'add tag'}</div>
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
					aria-describedby="tag-name-err"
					data-testid="tag-name"
				/></label
			>
			{#if errors.name}<span class="ferr" id="tag-name-err">{errors.name}</span>{/if}
		</div>
		<div class="field">
			<div class="frow">
				<label for="tag-color" class="k">color</label>
				<ColorField id="tag-color" bind:value={f.color} invalid={!!errors.color} describedby="tag-color-err" />
			</div>
			{#if errors.color}<span class="ferr" id="tag-color-err">{errors.color}</span>{/if}
		</div>
	</div>
	{#if serverError}<div class="server-error" role="alert">{serverError}</div>{/if}
	<div class="actions">
		<button type="submit" class="primary" disabled={saving} data-testid="save">save</button>
		<button type="button" onclick={oncancel}>cancel</button>
		<span class="hint">Enter saves · Esc cancels</span>
	</div>
</form>
