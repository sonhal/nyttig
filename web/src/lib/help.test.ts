import { describe, expect, it } from 'vitest';
import { feedSections, manageSections } from './help';
import { COMMANDS } from './command';
import { QUERY_KEYS } from './query';

// The overlay keys its lists by title and description, so both must be unique.
function unique(sections: { title: string; rows: { desc: string; keys: string[] }[] }[]) {
	const titles = sections.map((s) => s.title);
	expect(new Set(titles).size).toBe(titles.length);
	for (const s of sections) {
		const descs = s.rows.map((r) => r.desc);
		expect(new Set(descs).size, s.title).toBe(descs.length);
		for (const r of s.rows) expect(r.keys.length, r.desc).toBeGreaterThan(0);
	}
}

describe('help content', () => {
	it('has unique sections and rows', () => {
		unique(feedSections());
		unique(manageSections(['add', 'edit', 'delete', 'toggle', 'refresh']));
		unique(manageSections([]));
	});

	it('lists every command and query operator from their tables', () => {
		const feed = feedSections();
		const commands = feed.find((s) => s.title.startsWith('Commands'))!;
		expect(commands.rows.map((r) => r.keys[0]!.split(' ')[0])).toEqual(COMMANDS.map((c) => ':' + c.name));
		const query = feed.find((s) => s.title.startsWith('Query syntax'))!;
		expect(query.rows.map((r) => r.keys[0])).toEqual(QUERY_KEYS.map((q) => q.usage));
	});

	it('management views list the commands but not the query syntax', () => {
		const titles = manageSections(['add']).map((s) => s.title);
		expect(titles.some((t) => t.startsWith('Commands'))).toBe(true);
		expect(titles.some((t) => t.startsWith('Query syntax'))).toBe(false);
	});
});
