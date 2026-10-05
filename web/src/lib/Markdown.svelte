<!--
	Renders a digest body: the Markdown subset of markdown.ts, as elements and
	text nodes only. The body is untrusted (an LLM wrote it, and it can repeat
	the markup of the feed it read), so nothing here builds HTML: raw markup in
	the body is shown as the text it is, there are no images, and a link exists
	only where the parser accepted its target. [#123] links to an item only when
	it is one of the digest's linked items.
-->
<script lang="ts">
	import { parseMarkdown, type Block, type Inline } from './markdown';
	import { oneLine, safeLink } from './sanitize';
	import type { DigestItem } from './types';

	interface Props {
		source: string;
		/** The items the digest is based on: what [#id] may link to. */
		items?: readonly DigestItem[];
	}

	let { source, items = [] }: Props = $props();

	const blocks = $derived(parseMarkdown(source));
	const byId = $derived(new Map(items.map((it) => [it.item_id ?? '', it])));

	function ref(id: string): { href: string; title: string } | undefined {
		const it = byId.get(id);
		const href = safeLink(it?.link);
		return it && href ? { href, title: oneLine(it.title) } : undefined;
	}
</script>

{#snippet inl(nodes: Inline[])}
	{#each nodes as n, i (i)}
		{#if n.t === 'text'}{n.text}
		{:else if n.t === 'br'}<br />
		{:else if n.t === 'strong'}<strong>{@render inl(n.children)}</strong>
		{:else if n.t === 'em'}<em>{@render inl(n.children)}</em>
		{:else if n.t === 'code'}<code>{n.text}</code>
		{:else if n.t === 'link'}<a href={n.href} target="_blank" rel="noopener noreferrer">{@render inl(n.children)}</a>
		{:else if n.t === 'item'}
			{@const r = ref(n.id)}
			{#if r}<a class="ref" href={r.href} title={r.title} target="_blank" rel="noopener noreferrer" data-testid="item-ref">[#{n.id}]</a
				>{:else}[#{n.id}]{/if}
		{/if}
	{/each}
{/snippet}

{#snippet blk(nodes: Block[])}
	{#each nodes as b, i (i)}
		{#if b.t === 'heading'}
			{#if b.level === 1}<h3>{@render inl(b.children)}</h3>
			{:else if b.level === 2}<h4>{@render inl(b.children)}</h4>
			{:else}<h5>{@render inl(b.children)}</h5>{/if}
		{:else if b.t === 'para'}<p>{@render inl(b.children)}</p>
		{:else if b.t === 'list'}
			{#if b.ordered}
				<ol>{#each b.items as it, j (j)}<li>{@render blk(it)}</li>{/each}</ol>
			{:else}
				<ul>{#each b.items as it, j (j)}<li>{@render blk(it)}</li>{/each}</ul>
			{/if}
		{:else if b.t === 'quote'}<blockquote>{@render blk(b.children)}</blockquote>
		{:else if b.t === 'code'}<pre><code>{b.text}</code></pre>
		{:else}<hr />{/if}
	{/each}
{/snippet}

<div class="md" data-testid="digest-body">{@render blk(blocks)}</div>

<style>
	.md {
		overflow-wrap: anywhere;
	}
	.md :global(p) {
		margin: 0 0 8px;
	}
	.md :global(h3),
	.md :global(h4),
	.md :global(h5) {
		margin: 12px 0 4px;
		font-size: inherit;
		font-weight: normal;
		color: var(--fg-strong);
	}
	.md :global(h3)::before {
		content: '# ';
		color: var(--dim);
	}
	.md :global(h4)::before {
		content: '## ';
		color: var(--dim);
	}
	.md :global(h5)::before {
		content: '### ';
		color: var(--dim);
	}
	.md :global(strong) {
		color: var(--fg-strong);
	}
	.md :global(em) {
		font-style: italic;
	}
	.md :global(code) {
		color: var(--desc);
	}
	.md :global(pre) {
		margin: 0 0 8px;
		padding: 4px 1ch;
		overflow-x: auto;
		background: var(--bar);
		border-left: 2px solid var(--sel);
		white-space: pre;
	}
	.md :global(pre code) {
		color: var(--fg);
	}
	.md :global(ul),
	.md :global(ol) {
		margin: 0 0 8px;
		padding-left: 3ch;
	}
	.md :global(li > p) {
		margin: 0;
	}
	.md :global(blockquote) {
		margin: 0 0 8px;
		padding-left: 1.5ch;
		border-left: 2px solid var(--dim);
		color: var(--dim);
	}
	.md :global(hr) {
		border: none;
		border-top: 1px solid var(--sel);
		margin: 8px 0;
	}
	.md :global(a) {
		color: var(--accent);
	}
</style>
