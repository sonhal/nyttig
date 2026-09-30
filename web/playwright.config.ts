import { defineConfig, devices } from '@playwright/test';
import { BASE_URL } from './e2e/feeds.mjs';

// End-to-end tests against a real nyttigd and nyttig-api (see e2e/stack.mjs).
// Build the app first: "pnpm build && pnpm test:e2e".
//
// PLAYWRIGHT_CHROMIUM_EXECUTABLE runs a preinstalled Chromium instead of the
// one "playwright install" downloads, e.g. in a sandbox without downloads.
const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE || undefined;

export default defineConfig({
	testDir: 'e2e',
	// The tests share one daemon and database.
	workers: 1,
	fullyParallel: false,
	forbidOnly: !!process.env.CI,
	retries: 0,
	timeout: 30_000,
	expect: { timeout: 10_000 },
	reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
	use: {
		baseURL: BASE_URL,
		timezoneId: 'Europe/Oslo',
		trace: 'retain-on-failure',
		launchOptions: { executablePath }
	},
	projects: [
		{
			name: 'desktop',
			use: { ...devices['Desktop Chrome'], viewport: { width: 1280, height: 720 } }
		},
		{
			name: 'mobile',
			use: { ...devices['Pixel 7'] }
		}
	],
	webServer: {
		command: 'node e2e/stack.mjs',
		url: BASE_URL + '/api/health',
		timeout: 240_000,
		reuseExistingServer: false,
		stdout: 'pipe',
		stderr: 'pipe'
	}
});
