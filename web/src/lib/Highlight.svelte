<!--
	Text with the search words marked. The pieces are rendered as text nodes
	and <mark> elements, never as HTML, so feed text stays inert (see
	highlight.ts for how words are matched).
-->
<script lang="ts">
	import { segments } from './highlight';

	let { text, terms }: { text: string; terms: readonly string[] } = $props();

	const parts = $derived(segments(text, terms));
</script>

{#each parts as p, i (i)}{#if p.match}<mark>{p.text}</mark>{:else}{p.text}{/if}{/each}

<style>
	mark {
		background: rgb(86 156 214 / 30%);
		color: var(--fg-strong);
		border-radius: 2px;
	}
</style>
