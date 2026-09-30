import { describe, expect, it } from 'vitest';
import { parseCommand } from './command';

describe('parseCommand', () => {
	it.each([
		['sources', 'sources'],
		[':sources', 'sources'],
		['  :tags ', 'tags'],
		['RULES', 'rules'],
		['feed', 'feed'],
		['so', 'sources'],
		['s', 'sources'],
		['r', 'rules'],
		['ta', 'tags'],
		['f', 'feed'],
		['q', 'feed'],
		['src', 'sources']
	])('%j → %s', (input, view) => {
		expect(parseCommand(input)).toEqual({ ok: true, view });
	});

	it('rejects unknown and empty commands with a hint', () => {
		const unknown = parseCommand('help');
		expect(unknown.ok).toBe(false);
		if (!unknown.ok) expect(unknown.error).toContain('unknown command: help');
		const empty = parseCommand(':');
		expect(empty.ok).toBe(false);
		if (!empty.ok) expect(empty.error).toContain('sources');
	});
});
