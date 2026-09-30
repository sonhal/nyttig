// The ":" command line: ":sources", ":tags", ":rules" and ":feed" switch
// views. A unique prefix is enough (":so", ":r"), and ":q" goes back to
// the feed.

export type View = 'feed' | 'sources' | 'tags' | 'rules';

export const VIEWS: readonly View[] = ['feed', 'sources', 'tags', 'rules'];

/** The route of each view. */
export const VIEW_PATHS: Record<View, string> = {
	feed: '/',
	sources: '/sources',
	tags: '/tags',
	rules: '/rules'
};

const ALIASES: Record<string, View> = { q: 'feed', quit: 'feed', src: 'sources' };

export type CommandResult = { ok: true; view: View } | { ok: false; error: string };

export function parseCommand(input: string): CommandResult {
	const cmd = input.trim().replace(/^:+/, '').trim().toLowerCase();
	if (cmd === '') return { ok: false, error: 'commands: ' + VIEWS.join(' ') };
	const alias = ALIASES[cmd];
	if (alias) return { ok: true, view: alias };
	const matches = VIEWS.filter((v) => v.startsWith(cmd));
	const only = matches.length === 1 ? matches[0] : undefined;
	if (only) return { ok: true, view: only };
	if (matches.length > 1) return { ok: false, error: `ambiguous: ${cmd} (${matches.join(', ')})` };
	return { ok: false, error: `unknown command: ${cmd} (try ${VIEWS.join(', ')})` };
}
