<!--
	Edit a digest series: its name and what it covers. A series belongs to
	the assessor that created it and is not moved to another one.
-->
<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { hasErrors, validateSeries, type SeriesForm } from './forms';

	interface Props {
		initial: SeriesForm;
		onsave: (f: SeriesForm) => Promise<void>;
		oncancel: () => void;
	}

	let { initial, onsave, oncancel }: Props = $props();

	let f: SeriesForm = $state(untrack(() => ({ ...initial })));
	let submitted = $state(false);
	let saving = $state(false);
	let serverError = $state('');
	let form: HTMLFormElement | undefined = $state();

	const errors = $derived(submitted ? validateSeries(f) : {});

	onMount(() => form?.querySelector<HTMLInputElement>('input')?.focus());

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (saving) return;
		submitted = true;
		if (hasErrors(validateSeries(f))) {
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

<form class="mform" bind:this={form} onsubmit={submit} novalidate data-testid="series-form">
	<div class="title">edit series</div>
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
					aria-describedby="series-name-err"
					data-testid="series-name"
				/></label
			>
			{#if errors.name}<span class="ferr" id="series-name-err">{errors.name}</span>{/if}
		</div>
		<div class="field grow">
			<label
				><span class="k">about</span><input
					type="text"
					bind:value={f.description}
					maxlength="500"
					placeholder="what the series covers"
					autocomplete="off"
					autocapitalize="off"
					aria-invalid={!!errors.description}
					aria-describedby="series-desc-err"
					data-testid="series-description"
				/></label
			>
			{#if errors.description}<span class="ferr" id="series-desc-err">{errors.description}</span>{/if}
		</div>
	</div>
	{#if serverError}<div class="server-error" role="alert">{serverError}</div>{/if}
	<div class="actions">
		<button type="submit" class="primary" disabled={saving} data-testid="save">save</button>
		<button type="button" onclick={oncancel}>cancel</button>
		<span class="hint">Enter saves · Esc cancels</span>
	</div>
</form>
