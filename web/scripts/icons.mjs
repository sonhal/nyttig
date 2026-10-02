// Renders static/icon.svg to the PNG app icons the manifest and iOS need
// (iOS ignores SVG icons). Run "node scripts/icons.mjs" in web/ after
// changing icon.svg and commit the PNGs. Set CHROMIUM_PATH to use a
// Chromium other than the one Playwright downloads.
import { readFile } from 'node:fs/promises';
import { chromium } from '@playwright/test';

const svg = await readFile(new URL('../static/icon.svg', import.meta.url), 'utf8');
const sizes = { 'apple-touch-icon.png': 180, 'icon-192.png': 192, 'icon-512.png': 512 };

const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH });
const page = await browser.newPage();
for (const [name, size] of Object.entries(sizes)) {
	await page.setViewportSize({ width: size, height: size });
	await page.setContent(
		`<style>*{margin:0}svg{display:block;width:${size}px;height:${size}px}</style>${svg}`
	);
	await page.locator('svg').screenshot({ path: new URL(`../static/${name}`, import.meta.url).pathname });
}
await browser.close();
