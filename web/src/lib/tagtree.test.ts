import { describe, expect, it } from 'vitest';
import { buildTree, descendants, flatten, orphansOnDelete, treeOrder, wouldCycle } from './tagtree';
import type { Tag } from './types';

const t = (id: string, name: string, ...parent_ids: string[]): Tag => ({ id, name, parent_ids });

// cyber security -> CVE, linux security;  linux -> linux security
const diamond = [t('1', 'cyber security'), t('2', 'linux'), t('3', 'CVE', '1'), t('4', 'linux security', '1', '2')];

const names = (rows: { tag: Tag; depth: number; repeat: boolean }[]) =>
	rows.map((r) => `${r.depth}${r.repeat ? '~' : ':'}${r.tag.name}`);

describe('buildTree', () => {
	it('puts tags without parents at the root, in list order', () => {
		expect(buildTree([t('2', 'b'), t('1', 'a')]).map((n) => n.tag.name)).toEqual(['b', 'a']);
	});
	it('nests children, and a tag with two parents appears under both', () => {
		const tree = buildTree(diamond);
		expect(tree.map((n) => n.tag.name)).toEqual(['cyber security', 'linux']);
		expect(tree[0]!.children.map((n) => n.tag.name)).toEqual(['CVE', 'linux security']);
		expect(tree[1]!.children.map((n) => n.tag.name)).toEqual(['linux security']);
	});
	it('treats a parent that is not in the list as no parent', () => {
		expect(buildTree([t('1', 'a', '99')]).map((n) => n.tag.name)).toEqual(['a']);
	});
	it('handles tags without parent_ids', () => {
		expect(buildTree([{ id: '1', name: 'a' }])).toHaveLength(1);
	});
	it('cuts a cycle instead of looping', () => {
		const tree = buildTree([t('1', 'root'), t('2', 'a', '1', '3'), t('3', 'b', '2')]);
		expect(names(flatten(tree))).toEqual(['0:root', '1:a', '2:b']);
	});
});

describe('flatten', () => {
	it('lists each tag once at its first position by default', () => {
		expect(names(flatten(buildTree(diamond)))).toEqual(['0:cyber security', '1:CVE', '1:linux security', '0:linux']);
	});
	it('with repeats, shows the tag under every parent but expands it once', () => {
		const rows = flatten(buildTree(diamond), { repeats: true });
		expect(names(rows)).toEqual(['0:cyber security', '1:CVE', '1:linux security', '0:linux', '1~linux security']);
		expect(new Set(rows.map((r) => r.key)).size).toBe(rows.length);
	});
	it('keeps a deep chain in order', () => {
		const chain = [t('1', 'a'), t('2', 'b', '1'), t('3', 'c', '2'), t('4', 'd', '3')];
		expect(names(treeOrder(chain))).toEqual(['0:a', '1:b', '2:c', '3:d']);
	});
	it('orders a child that is listed before its parent', () => {
		expect(names(treeOrder([t('2', 'child', '1'), t('1', 'parent')]))).toEqual(['0:parent', '1:child']);
	});
	it('is empty for no tags', () => {
		expect(flatten(buildTree([]))).toEqual([]);
	});
});

describe('descendants', () => {
	it('collects every tag below, once, not the tag itself', () => {
		expect([...descendants(diamond, '1')].sort()).toEqual(['3', '4']);
		expect([...descendants(diamond, '2')]).toEqual(['4']);
		expect(descendants(diamond, '3').size).toBe(0);
	});
	it('reaches deep descendants', () => {
		const chain = [t('1', 'a'), t('2', 'b', '1'), t('3', 'c', '2')];
		expect([...descendants(chain, '1')].sort()).toEqual(['2', '3']);
	});
	it('terminates on a cycle and never lists the tag itself', () => {
		const loop = [t('1', 'a', '2'), t('2', 'b', '1')];
		expect([...descendants(loop, '1')]).toEqual(['2']);
	});
});

describe('wouldCycle', () => {
	it('rejects itself, a child and a deeper descendant', () => {
		const chain = [t('1', 'a'), t('2', 'b', '1'), t('3', 'c', '2')];
		expect(wouldCycle(chain, '1', '1')).toBe(true);
		expect(wouldCycle(chain, '1', '2')).toBe(true);
		expect(wouldCycle(chain, '1', '3')).toBe(true);
	});
	it('allows an unrelated tag, an ancestor, and a sibling branch', () => {
		expect(wouldCycle(diamond, '3', '2')).toBe(false);
		expect(wouldCycle(diamond, '4', '3')).toBe(false);
		expect(wouldCycle(diamond, '2', '1')).toBe(false);
	});
});

describe('orphansOnDelete', () => {
	it('lists children whose only parent is the tag', () => {
		expect(orphansOnDelete(diamond, '1').map((x) => x.name)).toEqual(['CVE']);
		expect(orphansOnDelete(diamond, '2')).toEqual([]);
		expect(orphansOnDelete(diamond, '3')).toEqual([]);
	});
});
