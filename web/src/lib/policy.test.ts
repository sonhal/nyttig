import { describe, expect, it } from 'vitest';

// Feed content is untrusted: it is only ever rendered as text, never as HTML.
const sources = import.meta.glob<string>(['./**/*.svelte', '../routes/**/*.svelte'], {
	query: '?raw',
	import: 'default',
	eager: true
});

describe('policy', () => {
	it('finds the components', () => {
		expect(Object.keys(sources).length).toBeGreaterThan(10);
		expect(Object.keys(sources)).toContain('./FeedRow.svelte');
		expect(Object.keys(sources)).toContain('../routes/+page.svelte');
	});

	it('no component uses {@html}', () => {
		for (const [file, src] of Object.entries(sources)) expect(src, file).not.toMatch(/\{@html\b/);
	});

	it('no component sets innerHTML', () => {
		for (const [file, src] of Object.entries(sources)) expect(src, file).not.toMatch(/\.innerHTML\s*=/);
	});
});
