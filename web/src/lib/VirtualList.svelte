<!--
	A virtual list with fixed row heights, and at most one expanded row whose
	detail block has a fixed height too. Every position is then simple
	arithmetic, which is also what visible-row tracking relies on.

	Heights come from the CSS variables --row-h and --detail-h on the list
	element (they differ per breakpoint) and are re-read on resize.
-->
<script lang="ts" generics="T">
	import type { Snippet } from 'svelte';
	import { onMount } from 'svelte';
	import type { HTMLAttributes } from 'svelte/elements';

	interface Props extends Omit<HTMLAttributes<HTMLDivElement>, 'onscroll' | 'children'> {
		items: T[];
		key: (item: T) => string;
		/** Index of the expanded row, or -1. */
		expanded?: number;
		/** Rows rendered beyond each edge of the viewport. */
		overscan?: number;
		row: Snippet<[T, number]>;
		detail?: Snippet<[T, number]>;
		empty?: Snippet;
		/** Called with the first and last (partially) visible row index. */
		onrange?: (first: number, last: number) => void;
	}

	let {
		items,
		key,
		expanded = -1,
		overscan = 10,
		row,
		detail,
		empty,
		onrange,
		class: className,
		...rest
	}: Props = $props();

	let el: HTMLDivElement | undefined = $state();
	let scrollTop = $state(0);
	let viewH = $state(0);
	let rowH = $state(20);
	let detailH = $state(120);

	const exp = $derived(detail && expanded >= 0 && expanded < items.length ? expanded : -1);
	const extra = $derived(exp >= 0 ? detailH : 0);
	const totalH = $derived(items.length * rowH + extra);

	function pos(i: number): number {
		return i * rowH + (exp >= 0 && i > exp ? detailH : 0);
	}

	function indexAt(y: number): number {
		let i: number;
		if (exp < 0) i = Math.floor(y / rowH);
		else {
			const boundary = (exp + 1) * rowH;
			if (y < boundary) i = Math.floor(y / rowH);
			else if (y < boundary + detailH) i = exp;
			else i = Math.floor((y - detailH) / rowH);
		}
		return Math.max(0, Math.min(i, items.length - 1));
	}

	const start = $derived(items.length ? Math.max(0, indexAt(scrollTop) - overscan) : 0);
	const end = $derived(items.length ? Math.min(items.length, indexAt(scrollTop + viewH) + 1 + overscan) : 0);
	const windowItems = $derived(items.slice(start, end));

	function measure() {
		if (!el) return;
		const cs = getComputedStyle(el);
		const r = parseFloat(cs.getPropertyValue('--row-h'));
		const d = parseFloat(cs.getPropertyValue('--detail-h'));
		if (r > 0) rowH = r;
		if (d > 0) detailH = d;
		viewH = el.clientHeight;
		scrollTop = el.scrollTop;
	}

	onMount(() => {
		measure();
		const ro = new ResizeObserver(measure);
		if (el) ro.observe(el);
		return () => ro.disconnect();
	});

	/** Calls onrange with the rows visible now. */
	export function reportRange(): void {
		if (!onrange || items.length === 0 || viewH === 0) return;
		onrange(indexAt(scrollTop), indexAt(scrollTop + viewH - 1));
	}

	// Report the visible range whenever it can have changed.
	$effect(reportRange);

	// Scroll anchoring: when rows are inserted or removed above the top of
	// the viewport, keep the same row at the top instead of letting the
	// content jump. At the very top, new rows scroll into view.
	let anchor: { key: string; offset: number } | null = null;
	let lastItems: T[] = [];
	$effect.pre(() => {
		const cur = items;
		if (cur === lastItems) return;
		const prev = lastItems;
		lastItems = cur;
		if (!el || !anchor || scrollTop <= 0 || prev.length === 0) return;
		const a = anchor;
		const i = cur.findIndex((it) => key(it) === a.key);
		if (i < 0) return;
		const target = pos(i) + a.offset;
		if (target !== scrollTop) {
			scrollTop = target;
			queueMicrotask(() => {
				if (el) el.scrollTop = target;
			});
		}
	});

	function onscroll() {
		if (!el) return;
		scrollTop = el.scrollTop;
		updateAnchor();
	}

	function updateAnchor() {
		if (items.length === 0) {
			anchor = null;
			return;
		}
		const i = indexAt(scrollTop);
		const it = items[i];
		anchor = it === undefined ? null : { key: key(it), offset: scrollTop - pos(i) };
	}

	$effect(() => {
		void items;
		void scrollTop;
		updateAnchor();
	});

	/** Scrolls the least amount that makes row i (and its detail) visible. */
	export function ensureVisible(i: number): void {
		if (!el || i < 0 || i >= items.length) return;
		const top = pos(i);
		const bottom = top + rowH + (i === exp ? detailH : 0);
		let next = scrollTop;
		if (top < next) next = top;
		else if (bottom > next + viewH) next = Math.min(top, bottom - viewH);
		if (next !== scrollTop) {
			el.scrollTop = next;
			scrollTop = el.scrollTop;
		}
	}

	/** Number of rows that fit in the viewport. */
	export function pageRows(): number {
		return Math.max(1, Math.floor(viewH / rowH));
	}

	export function focus(): void {
		el?.focus({ preventScroll: true });
	}
</script>

<div {...rest} class={['vlist', className]} bind:this={el} {onscroll}>
	{#if items.length === 0}
		{@render empty?.()}
	{:else}
		<div class="spacer" style:height="{totalH}px">
			<div class="window" style:transform="translateY({pos(start)}px)">
				{#each windowItems as item, j (key(item))}
					{@render row(item, start + j)}
					{#if start + j === exp && detail}
						<div class="detail">{@render detail(item, start + j)}</div>
					{/if}
				{/each}
			</div>
		</div>
	{/if}
</div>

<style>
	.vlist {
		overflow-y: auto;
		overflow-x: hidden;
		overscroll-behavior: contain;
		position: relative;
		outline: none;
		min-height: 0;
	}
	.spacer {
		position: relative;
	}
	.window {
		position: absolute;
		top: 0;
		left: 0;
		right: 0;
		will-change: transform;
	}
	.detail {
		height: var(--detail-h);
		overflow-y: auto;
		box-sizing: border-box;
	}
</style>
