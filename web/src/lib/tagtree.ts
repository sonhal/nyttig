// The tag tree: tags can have several parents (a DAG), and filtering by a
// tag also matches the tags below it. Pure functions over the tag list, used
// by the form, the tags view, the pickers and the filter sheet. The daemon
// is the authority on cycles; wouldCycle only lets the UI hide options it
// would refuse.

import type { Tag } from './types';

export interface TreeNode {
	tag: Tag;
	children: TreeNode[];
}

export interface FlatRow {
	tag: Tag;
	/** 0 for a root. */
	depth: number;
	/**
	 * True for the second and later appearances of a tag that has several
	 * parents. Only flatten(..., { repeats: true }) emits them, and a repeat
	 * has no children of its own: they are shown under the first appearance.
	 */
	repeat: boolean;
	/** Unique among the rows: the tag ID, plus the parent ID for a repeat. */
	key: string;
}

const idOf = (t: Tag) => t.id ?? '';

/** Parents that exist in the list; a dangling ID is ignored. */
function knownParents(t: Tag, byId: Map<string, Tag>): string[] {
	return (t.parent_ids ?? []).filter((p) => byId.has(p) && p !== idOf(t));
}

/**
 * The roots (tags with no parent) with their children below, siblings in
 * list order. A tag with several parents appears under each of them. A cycle
 * (which the daemon rejects) is cut where it closes, and tags only reachable
 * through one are left out.
 */
export function buildTree(tags: readonly Tag[]): TreeNode[] {
	const byId = new Map(tags.map((t) => [idOf(t), t]));
	const kids = new Map<string, Tag[]>();
	const roots: Tag[] = [];
	for (const t of tags) {
		const ps = knownParents(t, byId);
		if (ps.length === 0) roots.push(t);
		for (const p of ps) kids.set(p, [...(kids.get(p) ?? []), t]);
	}
	const build = (t: Tag, path: Set<string>): TreeNode => {
		const next = new Set(path).add(idOf(t));
		return {
			tag: t,
			children: (kids.get(idOf(t)) ?? []).filter((c) => !next.has(idOf(c))).map((c) => build(c, next))
		};
	};
	return roots.map((r) => build(r, new Set()));
}

/**
 * The tree as rows in depth-first order. By default every tag appears once,
 * at its first position (the pickers). With repeats, a tag with several
 * parents also appears under the others, marked repeat and without children.
 */
export function flatten(tree: readonly TreeNode[], opts: { repeats?: boolean } = {}): FlatRow[] {
	const rows: FlatRow[] = [];
	const seen = new Set<string>();
	const walk = (n: TreeNode, depth: number, parent: string) => {
		const id = idOf(n.tag);
		if (seen.has(id)) {
			if (opts.repeats) rows.push({ tag: n.tag, depth, repeat: true, key: `${id}@${parent}` });
			return;
		}
		seen.add(id);
		rows.push({ tag: n.tag, depth, repeat: false, key: id });
		for (const c of n.children) walk(c, depth + 1, id);
	};
	for (const r of tree) walk(r, 0, '');
	return rows;
}

/** Shorthand: the tags in tree order, each once. */
export function treeOrder(tags: readonly Tag[]): FlatRow[] {
	return flatten(buildTree(tags));
}

/** The IDs of every tag below id (not id itself). */
export function descendants(tags: readonly Tag[], id: string): Set<string> {
	const kids = new Map<string, string[]>();
	for (const t of tags) for (const p of t.parent_ids ?? []) kids.set(p, [...(kids.get(p) ?? []), idOf(t)]);
	const out = new Set<string>();
	const queue = [id];
	for (let cur = queue.shift(); cur !== undefined; cur = queue.shift()) {
		for (const c of kids.get(cur) ?? []) {
			if (c !== id && !out.has(c)) {
				out.add(c);
				queue.push(c);
			}
		}
	}
	return out;
}

/** True if making parent a parent of child would close a loop. */
export function wouldCycle(tags: readonly Tag[], child: string, parent: string): boolean {
	return child === parent || descendants(tags, child).has(parent);
}

/** Tags that have id as a parent and no other: they become top-level when id is deleted. */
export function orphansOnDelete(tags: readonly Tag[], id: string): Tag[] {
	return tags.filter((t) => (t.parent_ids ?? []).includes(id) && (t.parent_ids ?? []).length === 1);
}
