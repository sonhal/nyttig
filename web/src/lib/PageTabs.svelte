<!--
	The row of page tabs on top of every page but the feed: feed, sources,
	tags, rules, views, assessors, digests. The current page is bracketed on a
	desktop and filled on a phone.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { PAGES, type Page } from './command';
	import { pageHref } from './commandline.svelte';

	interface Props {
		page: Exclude<Page, 'feed'>;
		/** Counts shown next to the names. */
		counts?: Partial<Record<Page, number>>;
	}

	let { page, counts = {} }: Props = $props();

	let nav: HTMLElement | undefined = $state();

	// Seven tabs do not fit a phone: the row scrolls, and the current page
	// starts in view.
	onMount(() => {
		const cur = nav?.querySelector<HTMLElement>('[aria-current]');
		if (nav && cur) nav.scrollLeft = cur.offsetLeft - (nav.clientWidth - cur.offsetWidth) / 2;
	});
</script>

<nav class="tabs" aria-label="pages" bind:this={nav}>
	{#each PAGES as v (v)}
		<a
			href={pageHref(v)}
			class:current={v === page}
			aria-current={v === page ? 'page' : undefined}
			data-testid="nav-{v}"
			>{v}{#if counts[v] !== undefined}<span class="n">{' ' + counts[v]}</span>{/if}</a
		>
	{/each}
	<span class="hint">: command · q feed</span>
</nav>

<style>
	.tabs {
		display: flex;
		align-items: center;
		gap: 2ch;
		height: var(--bar-h);
		padding: 0 1ch;
		background: var(--bar-filter);
		white-space: nowrap;
		overflow: hidden;
	}
	.tabs a {
		color: var(--dim);
		text-decoration: none;
	}
	.tabs a.current {
		color: var(--fg-strong);
	}
	.tabs a.current::before {
		content: '[';
		color: var(--dim);
	}
	.tabs a.current::after {
		content: ']';
		color: var(--dim);
	}
	.n {
		color: var(--dim);
	}
	.hint {
		margin-left: auto;
		color: var(--dim);
	}

	@media (max-width: 719.98px) {
		.tabs {
			gap: 0;
			padding: env(safe-area-inset-top) 0 0;
			height: calc(var(--bar-h) + env(safe-area-inset-top));
		}
		.tabs {
			overflow-x: auto;
			scrollbar-width: none;
		}
		.tabs::-webkit-scrollbar {
			display: none;
		}
		.tabs a {
			flex: 1 0 auto;
			display: flex;
			align-items: center;
			justify-content: center;
			height: 100%;
			padding: 0 1.2ch;
			white-space: pre;
		}
		.tabs a.current {
			background: var(--sel);
		}
		.tabs a.current::before,
		.tabs a.current::after {
			content: none;
		}
		.hint {
			display: none;
		}
	}
</style>
