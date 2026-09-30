// A local RSS server for the end-to-end tests. Items can be added at
// runtime (POST /add?feed=alpha&title=...) to exercise live pushes, and it
// counts requests for the tracking pixel an item embeds, which the web app
// must never load.

import http from 'node:http';

export const FEED_PORT = Number(process.env.NYTTIG_E2E_FEED_PORT ?? 7811);
/** The proxy the browser talks to, like Caddy in production. */
export const BASE_PORT = Number(process.env.NYTTIG_E2E_BASE_PORT ?? 7812);
/** nyttig-web: the SvelteKit Node server ("node build"). */
export const WEB_PORT = BASE_PORT + 1;
/** nyttig-api. */
export const API_PORT = BASE_PORT + 2;
export const FEEDS_URL = `http://127.0.0.1:${FEED_PORT}`;
export const BASE_URL = `http://127.0.0.1:${BASE_PORT}`;

const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

function hoursAgo(h) {
	return new Date(Date.now() - h * 3600_000).toUTCString();
}

const feeds = {
	alpha: [
		{ guid: 'a1', title: 'Linux 6.18 released', link: 'https://www.kernel.org/linux-6.18', ago: 1, description: 'Linus announced the release of <b>Linux 6.18</b>.' },
		{ guid: 'a2', title: 'Rust for Linux status', link: 'https://lwn.net/Articles/rust', ago: 2, description: 'An overview of where Rust support stands.' },
		{
			guid: 'a3',
			title: 'Evil item',
			link: 'javascript:window.__pwned=1',
			ago: 3,
			// nyttigd strips tags and then unescapes entities, so the
			// escaped markup here reaches the browser as real HTML.
			description:
				'<b>bold</b> ' +
				esc(
					`<img src="http://127.0.0.1:${FEED_PORT}/pixel.png" onerror="window.__pwned=2">` +
						'<script>window.__pwned=3</script><style>body{display:none}</style>'
				) +
				'Tracked text'
		}
	],
	beta: [
		{ guid: 'b1', title: 'Go 1.26.8 security release', link: 'https://go.dev/blog/go1.26.8', ago: 4, description: 'Fixes GO-2026-6443 in net/http.' },
		{ guid: 'b2', title: 'Old post', link: 'https://beta.example/old', ago: 48, description: '' }
	]
};

function rss(name) {
	const items = feeds[name]
		.map(
			(it) => `<item>
<title>${esc(it.title)}</title>
<link>${esc(it.link)}</link>
<guid>${esc(it.guid)}</guid>
<pubDate>${it.pubDate ?? hoursAgo(it.ago)}</pubDate>
<description>${esc(it.description)}</description>
</item>`
		)
		.join('\n');
	return `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>${name}</title><link>https://${name}.example</link><description>${name}</description>
${items}
</channel></rss>`;
}

export function startFeedServer() {
	let pixelHits = 0;
	let added = 0;
	const server = http.createServer((req, res) => {
		const url = new URL(req.url ?? '/', FEEDS_URL);
		const m = url.pathname.match(/^\/(\w+)\.xml$/);
		if (m && feeds[m[1]]) {
			res.writeHead(200, { 'Content-Type': 'application/rss+xml' });
			res.end(rss(m[1]));
			return;
		}
		if (url.pathname === '/pixel.png') {
			pixelHits++;
			res.writeHead(404);
			res.end();
			return;
		}
		if (url.pathname === '/pixel-hits') {
			res.writeHead(200, { 'Content-Type': 'application/json' });
			res.end(JSON.stringify({ hits: pixelHits }));
			return;
		}
		if (url.pathname === '/add' && req.method === 'POST') {
			const feed = url.searchParams.get('feed') ?? 'alpha';
			const title = url.searchParams.get('title') ?? `Live item ${added}`;
			added++;
			feeds[feed].unshift({
				guid: `live-${added}-${Date.now()}`,
				title,
				link: `https://${feed}.example/live/${added}`,
				pubDate: new Date().toUTCString(),
				description: 'Pushed while the page was open.'
			});
			res.writeHead(204);
			res.end();
			return;
		}
		res.writeHead(404);
		res.end();
	});
	return new Promise((resolve) => server.listen(FEED_PORT, '127.0.0.1', () => resolve(server)));
}
