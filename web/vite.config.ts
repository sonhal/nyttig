import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vitest/config';

export default defineConfig({
	plugins: [sveltekit()],
	build: {
		// Never inline assets as data: URIs; the CSP only allows 'self'.
		assetsInlineLimit: 0
	},
	server: {
		// "pnpm dev": run nyttig-api with --origin http://localhost:5173.
		// The Host and Origin headers pass through unchanged, so its Host
		// and CSRF checks see the dev server's origin.
		proxy: {
			'/api': { target: 'http://127.0.0.1:7070' }
		}
	},
	test: {
		include: ['src/**/*.test.ts'],
		environment: 'node'
	}
});
