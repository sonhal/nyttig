// A minimal stand-in for Caddy in the end-to-end tests: /api/* goes to
// nyttig-api and everything else to the SvelteKit Node server, with the
// Host header passed through unchanged (as Caddy does) and responses
// streamed, so Server-Sent Events arrive as they are sent.

import http from 'node:http';

/**
 * @param {number} port where the proxy listens
 * @param {{ api: number, web: number }} upstream ports on 127.0.0.1
 */
export function startProxy(port, upstream) {
	const server = http.createServer((req, res) => {
		const url = req.url ?? '/';
		const target = url === '/api' || url.startsWith('/api/') ? upstream.api : upstream.web;
		const up = http.request(
			{ host: '127.0.0.1', port: target, method: req.method, path: url, headers: req.headers },
			(upRes) => {
				res.writeHead(upRes.statusCode ?? 502, upRes.headers);
				upRes.pipe(res);
			}
		);
		up.on('error', () => {
			if (!res.headersSent) res.writeHead(502);
			res.end();
		});
		// The browser going away (e.g. closing an EventSource) must close
		// the upstream request too.
		res.on('close', () => up.destroy());
		req.pipe(up);
	});
	return new Promise((resolve) => server.listen(port, '127.0.0.1', () => resolve(server)));
}
