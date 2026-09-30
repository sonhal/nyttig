// The help overlay's content: the keymap's tables (keymap.ts), the command
// table (command.ts) and the query operators (query.ts), put together. Every
// row comes from one of those tables, so the help cannot drift from what the
// keys, commands and query bar actually do.

import { COMMANDS } from './command';
import { feedHelp, manageHelp, type HelpSection, type Tool } from './keymap';
import { QUERY_KEYS } from './query';

function commandHelp(): HelpSection {
	return {
		title: 'Commands (:)',
		rows: COMMANDS.map((c) => ({ keys: [':' + c.name + (c.args ? ' ' + c.args : '')], desc: c.desc }))
	};
}

function queryHelp(): HelpSection {
	return { title: 'Query syntax (/)', rows: QUERY_KEYS.map((q) => ({ keys: [q.usage], desc: q.desc })) };
}

/** The feed: every key, the query syntax and the commands. */
export function feedSections(): HelpSection[] {
	return [...feedHelp(), queryHelp(), commandHelp()];
}

/** A management view: the keys it has, and the commands. */
export function manageSections(tools: readonly Tool['type'][]): HelpSection[] {
	return [...manageHelp(tools), commandHelp()];
}
