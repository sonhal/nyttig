<!--
	Add or edit an assessor: a name, what its score means (shown with the
	scores) and a color for its chips.
-->
<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import ColorField from './ColorField.svelte';
	import { hasErrors, validateAssessor, type AssessorForm } from './forms';

	interface Props {
		initial: AssessorForm;
		editing: boolean;
		onsave: (f: AssessorForm) => Promise<void>;
		oncancel: () => void;
	}

	let { initial, editing, onsave, oncancel }: Props = $props();

	let f: AssessorForm = $state(untrack(() => ({ ...initial })));
	let submitted = $state(false);
	let saving = $state(false);
	let serverError = $state('');
	let form: HTMLFormElement | undefined = $state();

	const errors = $derived(submitted ? validateAssessor(f) : {});

	onMount(() => form?.querySelector<HTMLInputElement>('input')?.focus());

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (saving) return;
		submitted = true;
		if (hasErrors(validateAssessor(f))) {
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

<form class="mform" bind:this={form} onsubmit={submit} novalidate data-testid="assessor-form">
	<div class="title">{editing ? 'edit assessor' : 'add assessor'}</div>
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
					aria-describedby="assessor-name-err"
					data-testid="assessor-name"
				/></label
			>
			{#if errors.name}<span class="ferr" id="assessor-name-err">{errors.name}</span>{/if}
		</div>
		<div class="field grow">
			<label
				><span class="k">scale</span><input
					type="text"
					bind:value={f.description}
					maxlength="500"
					placeholder="what the score means, e.g. importance for the tag, 0-1"
					autocomplete="off"
					autocapitalize="off"
					aria-invalid={!!errors.description}
					aria-describedby="assessor-desc-err"
					data-testid="assessor-description"
				/></label
			>
			{#if errors.description}<span class="ferr" id="assessor-desc-err">{errors.description}</span>{/if}
		</div>
		<div class="field">
			<div class="frow">
				<label for="assessor-color" class="k">color</label>
				<ColorField id="assessor-color" bind:value={f.color} invalid={!!errors.color} describedby="assessor-color-err" />
			</div>
			{#if errors.color}<span class="ferr" id="assessor-color-err">{errors.color}</span>{/if}
		</div>
	</div>
	{#if serverError}<div class="server-error" role="alert">{serverError}</div>{/if}
	<div class="actions">
		<button type="submit" class="primary" disabled={saving} data-testid="save">save</button>
		<button type="button" onclick={oncancel}>cancel</button>
		<span class="hint">Enter saves · Esc cancels</span>
	</div>
</form>
