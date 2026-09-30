<!--
	A color as #RRGGBB text (keyboard first) with a native picker next to
	it. The text is the value; an empty value means no color.
-->
<script lang="ts">
	import { safeColor } from './sanitize';

	interface Props {
		value: string;
		id: string;
		invalid: boolean;
		describedby?: string;
	}

	let { value = $bindable(), id, invalid, describedby }: Props = $props();

	// The picker needs some color; show the dim text color for "none".
	const pick = $derived(safeColor(value.trim())?.toLowerCase() ?? '#808080');
</script>

<span class="color">
	<input
		{id}
		type="text"
		bind:value
		placeholder="#RRGGBB"
		maxlength="7"
		autocomplete="off"
		autocapitalize="off"
		spellcheck="false"
		aria-invalid={invalid}
		aria-describedby={describedby}
	/>
	<input
		type="color"
		value={pick}
		oninput={(e) => (value = e.currentTarget.value.toUpperCase())}
		aria-label="pick a color"
		tabindex={-1}
	/>
</span>

<style>
	.color {
		display: inline-flex;
		align-items: center;
		gap: 0.5ch;
		min-width: 0;
	}
	input[type='text'] {
		width: 9ch;
	}
	input[type='color'] {
		width: 20px;
		height: 20px;
		padding: 0;
		border: 1px solid var(--sel);
		background: none;
		cursor: pointer;
	}
	@media (max-width: 719.98px) {
		.color {
			display: flex;
			gap: 8px;
		}
		input[type='text'] {
			flex: 1;
			width: auto;
		}
		input[type='color'] {
			width: 40px;
			height: 40px;
		}
	}
</style>
