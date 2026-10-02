<!--
	Add or edit a tag (UpdateTag keeps its rules and item assignments). The
	parents are a list of checkboxes in tree order; the tag itself and its
	descendants are left out, since the daemon would refuse them as cycles.
-->
<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import ColorField from './ColorField.svelte';
	import { hasErrors, validateTag, type TagForm } from './forms';
	import { oneLine, safeColor } from './sanitize';
	import { treeOrder, wouldCycle } from './tagtree';
	import type { Tag } from './types';

	interface Props {
		initial: TagForm;
		editing: boolean;
		/** All tags, for the parent choices. */
		tags: Tag[];
		/** The tag being edited, if any. */
		selfId?: string;
		onsave: (f: TagForm) => Promise<void>;
		oncancel: () => void;
	}

	let { initial, editing, tags, selfId, onsave, oncancel }: Props = $props();

	let f: TagForm = $state(untrack(() => ({ ...initial })));
	let submitted = $state(false);
	let saving = $state(false);
	let serverError = $state('');
	let form: HTMLFormElement | undefined = $state();

	const errors = $derived(submitted ? validateTag(f) : {});
	const parentChoices = $derived(
		treeOrder(tags).filter((r) => !selfId || !wouldCycle(tags, selfId, r.tag.id ?? ''))
	);

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
	{#if parentChoices.length > 0}
		<fieldset class="parents" data-testid="tag-parents" aria-describedby="tag-parents-err">
			<legend class="k">parents (filtering by a parent includes this tag)</legend>
			<div class="choices">
				{#each parentChoices as r (r.key)}
					<label class="choice" style:padding-left="{r.depth * 2}ch"
						><input type="checkbox" value={r.tag.id} bind:group={f.parent_ids} /><span
							style:color={safeColor(r.tag.color)}>{oneLine(r.tag.name)}</span
						></label
					>
				{/each}
			</div>
			{#if errors.parent_ids}<span class="ferr" id="tag-parents-err">{errors.parent_ids}</span>{/if}
		</fieldset>
	{/if}
	{#if serverError}<div class="server-error" role="alert">{serverError}</div>{/if}
	<div class="actions">
		<button type="submit" class="primary" disabled={saving} data-testid="save">save</button>
		<button type="button" onclick={oncancel}>cancel</button>
		<span class="hint">Enter saves · Esc cancels</span>
	</div>
</form>

<style>
	.parents {
		margin: 0;
		padding: 0;
		border: 0;
		min-width: 0;
	}
	.parents legend {
		padding: 0;
	}
	.choices {
		display: flex;
		flex-direction: column;
		max-height: 8lh;
		overflow-y: auto;
	}
	.choice {
		display: flex;
		align-items: center;
		gap: 1ch;
		min-height: 20px;
	}
</style>
