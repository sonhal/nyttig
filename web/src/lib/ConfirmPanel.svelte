<!--
	A delete confirmation that spells out what else goes with it (RemoveTag
	and RemoveSource cascade). y or Enter confirms, n or Esc cancels.
-->
<script lang="ts">
	interface Props {
		title: string;
		/** What the delete takes with it, one consequence per line. */
		lines: string[];
		busy: boolean;
		error: string;
		onconfirm: () => void;
		oncancel: () => void;
	}

	let { title, lines, busy, error, onconfirm, oncancel }: Props = $props();
</script>

<div class="mform" role="alertdialog" aria-label={title} data-testid="confirm">
	<div class="question">{title}</div>
	{#each lines as l, i (i)}<div class="line">{l}</div>{/each}
	{#if error}<div class="server-error" role="alert">{error}</div>{/if}
	<div class="actions">
		<button type="button" class="danger" disabled={busy} onclick={onconfirm} data-testid="confirm-delete"
			>delete</button
		>
		<button type="button" onclick={oncancel}>cancel</button>
		<span class="hint">y deletes · n cancels</span>
	</div>
</div>

<style>
	.question {
		color: var(--fg-strong);
		overflow-wrap: anywhere;
	}
	.line {
		color: var(--error);
		overflow-wrap: anywhere;
	}
</style>
