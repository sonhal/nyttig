<!--
	The help overlay: every key of the current view, generated from the keymap
	tables (see help.ts). The view's global key handler closes it on q, Esc
	and ?; on a phone the close button and the scrim do.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import type { HelpSection } from './keymap';

	interface Props {
		title: string;
		sections: HelpSection[];
		onclose: () => void;
	}

	let { title, sections, onclose }: Props = $props();

	let panel: HTMLDivElement | undefined = $state();
	onMount(() => panel?.focus({ preventScroll: true }));
</script>

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="scrim" onclick={onclose}></div>
<div
	bind:this={panel}
	class="help"
	role="dialog"
	aria-modal="true"
	aria-label={title}
	tabindex="-1"
	data-testid="help"
>
	<header>
		<h2>{title}</h2>
		<span class="hint">q / Esc closes</span>
		<button type="button" class="close" onclick={onclose} aria-label="close help" data-testid="help-close"
			>×</button
		>
	</header>
	<div class="sections">
		{#each sections as s (s.title)}
			<section>
				<h3>{s.title}</h3>
				<dl>
					{#each s.rows as r (r.desc)}
						<div class="row">
							<dt>
								{#each r.keys as k, i (i)}<kbd>{k}</kbd>{/each}
							</dt>
							<dd>{r.desc}</dd>
						</div>
					{/each}
				</dl>
			</section>
		{/each}
	</div>
</div>

<style>
	.scrim {
		position: fixed;
		inset: 0;
		background: rgb(0 0 0 / 60%);
		z-index: 30;
	}
	.help {
		position: fixed;
		z-index: 31;
		inset: 24px max(24px, calc(50vw - 62ch));
		display: flex;
		flex-direction: column;
		background: var(--bar);
		border: 1px solid var(--sel);
		border-radius: 6px;
		outline: none;
		overflow: hidden;
	}
	header {
		display: flex;
		align-items: center;
		gap: 2ch;
		padding: 4px 1ch;
		border-bottom: 1px solid var(--sel);
	}
	h2 {
		margin: 0;
		font-size: inherit;
		color: var(--fg-strong);
	}
	.hint {
		color: var(--dim);
	}
	.close {
		margin-left: auto;
		width: 24px;
		height: 24px;
		line-height: 20px;
		font-size: 18px;
		background: none;
		border: 1px solid var(--sel);
		border-radius: 4px;
		cursor: pointer;
	}
	.sections {
		overflow-y: auto;
		padding: 4px 1ch 12px;
		columns: 2 44ch;
		column-gap: 4ch;
	}
	section {
		break-inside: avoid;
		margin-bottom: 8px;
	}
	h3 {
		margin: 4px 0 0;
		font-size: inherit;
		font-weight: normal;
		color: var(--accent);
	}
	dl {
		margin: 0;
	}
	.row {
		display: grid;
		grid-template-columns: 24ch minmax(0, 1fr);
		gap: 0 2ch;
	}
	dt {
		display: flex;
		flex-wrap: wrap;
		gap: 0 1ch;
		color: var(--unviewed);
	}
	dd {
		margin: 0;
		min-width: 0;
		overflow-wrap: anywhere;
	}
	kbd {
		font: inherit;
		white-space: nowrap;
	}
	@media (max-width: 719.98px) {
		.help {
			inset: 0;
			border-radius: 0;
			border: none;
			padding-top: env(safe-area-inset-top);
			padding-bottom: env(safe-area-inset-bottom);
		}
		header {
			padding: 4px 8px;
			min-height: 44px;
		}
		.hint {
			display: none;
		}
		.close {
			width: 44px;
			height: 44px;
			font-size: 24px;
		}
		.sections {
			columns: 1;
			padding: 4px 8px 16px;
		}
		.row {
			grid-template-columns: 1fr;
			padding: 2px 0;
		}
		dd {
			color: var(--dim);
		}
	}
</style>
