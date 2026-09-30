<!--
	The ":" command line, drawn over the status bar like vim's. The view's
	global key handler runs it on Enter and closes it on Esc.
-->
<script lang="ts">
	import type { CommandLine } from './commandline.svelte';

	let { cl }: { cl: CommandLine } = $props();

	let input: HTMLInputElement | undefined = $state();

	$effect(() => {
		input?.focus();
	});
</script>

{#if cl.open}
	<div class="cmd" data-testid="command-line">
		<label>
			<span aria-hidden="true">:</span>
			<span class="visually-hidden">Command</span>
			<input
				bind:this={input}
				bind:value={cl.text}
				type="text"
				autocomplete="off"
				autocapitalize="off"
				spellcheck="false"
				enterkeyhint="go"
				maxlength="64"
				data-testid="command"
				oninput={() => (cl.error = '')}
				onblur={() => cl.cancel()}
			/>
		</label>
		{#if cl.error}<span class="error" role="alert">{cl.error}</span>{/if}
	</div>
{/if}

<style>
	.cmd {
		position: fixed;
		left: 0;
		right: 0;
		bottom: 0;
		z-index: 20;
		display: flex;
		align-items: center;
		gap: 2ch;
		height: calc(var(--bar-h) + env(safe-area-inset-bottom));
		padding: 0 1ch env(safe-area-inset-bottom);
		background: var(--bar);
		white-space: nowrap;
	}
	label {
		display: flex;
		flex: 1;
		min-width: 0;
	}
	input {
		flex: 1;
		min-width: 0;
		background: transparent;
		border: none;
		outline: none;
		padding: 0;
		color: var(--fg-strong);
		caret-color: var(--unviewed);
	}
	.error {
		color: var(--error);
		overflow: hidden;
		text-overflow: ellipsis;
	}
	@media (max-width: 719.98px) {
		input {
			font-size: 16px;
		}
	}
</style>
