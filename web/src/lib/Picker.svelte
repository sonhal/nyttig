<!--
	The S/T picker: a filter input and a list of sources or tags. The feed's
	global key handler turns Enter, Esc and the arrows into actions (mode
	"picker"); typing goes to the input.
-->
<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { segmentsAt } from './highlight';
	import type { Picker } from './picker.svelte';
	import { oneLine, safeColor } from './sanitize';

	interface Props {
		picker: Picker;
		onpick: (id: string) => void;
		onclose: () => void;
	}

	let { picker, onpick, onclose }: Props = $props();

	let input: HTMLInputElement | undefined = $state();
	let list: HTMLUListElement | undefined = $state();
	onMount(() => input?.focus());

	// Keep the highlighted entry in view.
	$effect(() => {
		void picker.active;
		void tick().then(() => list?.querySelector('[aria-selected=true]')?.scrollIntoView({ block: 'nearest' }));
	});
</script>

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="scrim" onclick={onclose}></div>
<div class="picker" role="dialog" aria-modal="true" aria-label="Pick a {picker.kind}" data-testid="picker">
	<label>
		<span class="k">{picker.kind}:</span>
		<input
			bind:this={input}
			bind:value={picker.query}
			type="text"
			autocomplete="off"
			autocapitalize="off"
			spellcheck="false"
			maxlength="64"
			role="combobox"
			aria-expanded="true"
			aria-controls="picker-list"
			aria-activedescendant={picker.matches.length ? 'pick-' + picker.active : undefined}
			data-testid="picker-input"
			oninput={() => picker.typed()}
		/>
	</label>
	<ul id="picker-list" role="listbox" bind:this={list}>
		{#each picker.matches as m, i (m.item.id)}
			<!-- Keys are handled by the view; a pointer must not take focus from the input. -->
			<!-- svelte-ignore a11y_click_events_have_key_events -->
			<li
				id="pick-{i}"
				role="option"
				aria-selected={i === picker.active}
				class:active={i === picker.active}
				onpointerdown={(e) => e.preventDefault()}
				onclick={() => onpick(m.item.id)}
			>
				<span class="label" style:color={safeColor(m.item.color)} style:padding-left="{(m.item.depth ?? 0) * 2}ch"
					>{#each segmentsAt(oneLine(m.item.label), m.at) as s, j (j)}{#if s.match}<mark>{s.text}</mark
							>{:else}{s.text}{/if}{/each}</span
				>
			</li>
		{:else}
			<li class="none">no match</li>
		{/each}
	</ul>
</div>

<style>
	.scrim {
		position: fixed;
		inset: 0;
		background: rgb(0 0 0 / 50%);
		z-index: 30;
	}
	.picker {
		position: fixed;
		z-index: 31;
		top: 48px;
		left: 50%;
		transform: translateX(-50%);
		width: min(48ch, calc(100vw - 16px));
		max-height: calc(100dvh - 96px);
		display: flex;
		flex-direction: column;
		background: var(--bar);
		border: 1px solid var(--sel);
		border-radius: 6px;
		overflow: hidden;
	}
	label {
		display: flex;
		align-items: center;
		gap: 1ch;
		padding: 0 1ch;
		height: 24px;
		border-bottom: 1px solid var(--sel);
	}
	.k {
		color: var(--dim);
	}
	input {
		flex: 1;
		min-width: 0;
		background: transparent;
		border: none;
		outline: none;
		color: var(--fg-strong);
		caret-color: var(--unviewed);
	}
	ul {
		margin: 0;
		padding: 0;
		list-style: none;
		overflow-y: auto;
		max-height: calc(10 * 20px);
	}
	li {
		height: 20px;
		padding: 0 1ch;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		cursor: default;
	}
	li.active {
		background: var(--sel);
	}
	li.none {
		color: var(--dim);
	}
	.label {
		color: var(--fg);
	}
	mark {
		background: none;
		color: var(--unviewed);
		text-decoration: underline;
	}
	@media (max-width: 719.98px) {
		label {
			height: 44px;
		}
		input {
			font-size: 16px;
		}
		li {
			height: 44px;
			line-height: 44px;
		}
		ul {
			max-height: none;
		}
	}
</style>
