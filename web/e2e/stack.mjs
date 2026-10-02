// Starts the end-to-end stack, all on loopback with state in a temporary
// directory: a local feed server, a real nyttigd, a real nyttig-api, the
// SvelteKit Node server ("node build") and a proxy in front of the last two
// that routes like the example Caddyfile. Playwright runs this as its
// webServer and waits for /api/health through the proxy.
//
//   node e2e/stack.mjs
//
// The binaries are built from the repository unless NYTTIG_BIN_DIR points
// at a directory that already has nyttigd and nyttig-api. The web app must
// have been built first ("pnpm build").

import { spawn, execFileSync } from 'node:child_process';
import { existsSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { API_PORT, FEED_PORT, WEB_PORT, BASE_PORT, BASE_URL, startFeedServer } from './feeds.mjs';
import { startProxy } from './proxy.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const repo = path.resolve(here, '../..');

if (!existsSync(path.join(here, '../build/index.js'))) {
	console.error('stack: the web app is not built; run "pnpm build" in web/ first');
	process.exit(1);
}

const tmp = mkdtempSync(path.join(tmpdir(), 'nyttig-e2e-'));
let binDir = process.env.NYTTIG_BIN_DIR;
if (!binDir) {
	binDir = path.join(tmp, 'bin');
	console.log('stack: building nyttigd and nyttig-api');
	execFileSync('go', ['build', '-tags', 'sqlite_fts5', '-o', binDir + '/', './cmd/nyttigd', './cmd/nyttig-api'], {
		cwd: repo,
		stdio: 'inherit'
	});
}

const feeds = await startFeedServer();
const proxy = await startProxy(BASE_PORT, { api: API_PORT, web: WEB_PORT });

const socket = path.join(tmp, 'nyttig.sock');
const feed = (name) => `http://127.0.0.1:${FEED_PORT}/${name}.xml`;
writeFileSync(
	path.join(tmp, 'config.toml'),
	`
[[sources]]
name = "Alpha News"
url = "${feed('alpha')}"
refresh_sec = 3600
type = "rss"
color = "#FF6600"
abbreviation = "ALP"

[[sources]]
name = "Beta Blog"
url = "${feed('beta')}"
refresh_sec = 3600
type = "rss"
abbreviation = "BET"

[[tags]]
name = "rust"
color = "#CE422B"

[[tags]]
name = "linux"
color = "#FCC624"

[[tag_rules]]
tag = "rust"
pattern = "(?i)\\\\brust\\\\b"
field = "title"

[[tag_rules]]
tag = "linux"
pattern = "(?i)\\\\blinux\\\\b"
field = "title"
`
);

const children = [];
function start(name, cmd, args, env = {}) {
	const child = spawn(cmd, args, {
		stdio: ['ignore', 'inherit', 'inherit'],
		env: { ...process.env, ...env }
	});
	child.on('exit', (code, signal) => {
		if (!stopping) {
			console.error(`stack: ${name} exited (${code ?? signal})`);
			stop(1);
		}
	});
	children.push(child);
}

let stopping = false;
function stop(code = 0) {
	if (stopping) return;
	stopping = true;
	for (const c of children) c.kill('SIGTERM');
	feeds.close();
	proxy.close();
	setTimeout(() => {
		rmSync(tmp, { recursive: true, force: true });
		process.exit(code);
	}, 500);
}
process.on('SIGINT', () => stop());
process.on('SIGTERM', () => stop());

start('nyttigd', path.join(binDir, 'nyttigd'), [
	'--socket',
	socket,
	'--db-path',
	path.join(tmp, 'nyttig.db'),
	'--config',
	path.join(tmp, 'config.toml'),
	'--log-level',
	'warn'
]);
start('nyttig-api', path.join(binDir, 'nyttig-api'), [
	'--listen',
	`127.0.0.1:${API_PORT}`,
	'--origin',
	BASE_URL,
	'--socket',
	socket,
	'--log-level',
	'warn'
]);
start('nyttig-web', process.execPath, [path.join(here, '../build')], {
	HOST: '127.0.0.1',
	PORT: String(WEB_PORT),
	ORIGIN: BASE_URL
});
console.log(`stack: ${BASE_URL} (api :${API_PORT}, web :${WEB_PORT}), feeds on :${FEED_PORT}`);
