const { chromium } = require('playwright-core');

(async () => {
  const browser = await chromium.launch({
    executablePath: '/home/wangyang.49/.cache/ms-playwright/chromium-1228/chrome-linux64/chrome',
    args: ['--no-sandbox', '--disable-gpu'],
  });
  const page = await browser.newPage({ viewport: { width: 1280, height: 860 } });
  page.on('console', (msg) => {
    if (msg.type() === 'error') console.log('console-error:', msg.text());
  });
  await page.goto('http://127.0.0.1:1420/', { waitUntil: 'networkidle' });
  await page.screenshot({ path: '/data00/home/wangyang.49/codex-cpa-bridge/.qa/overview.png', fullPage: true });

  const summary = await page.evaluate(() => ({
    title: document.querySelector('h1')?.textContent,
    ready: document.body.innerText,
    toggles: document.querySelectorAll('.toggle').length,
  }));
  console.log('overview:', JSON.stringify(summary, null, 2).slice(0, 2000));

  await page.getByRole('button', { name: 'Models' }).click();
  await page.screenshot({ path: '/data00/home/wangyang.49/codex-cpa-bridge/.qa/models-before.png', fullPage: true });
  const before = await page.getByRole('switch', { name: 'Show GPT 5.6 Terra' }).getAttribute('aria-checked');
  await page.getByRole('switch', { name: 'Show GPT 5.6 Terra' }).click();
  const after = await page.getByRole('switch', { name: 'Show GPT 5.6 Terra' }).getAttribute('aria-checked');
  console.log('toggle:', before, '->', after);
  await page.getByLabel('Filter models').fill('terra');
  await page.screenshot({ path: '/data00/home/wangyang.49/codex-cpa-bridge/.qa/models-filtered.png', fullPage: true });
  const filteredCount = await page.locator('.model-row').count();
  console.log('filtered rows:', filteredCount);

  await page.getByRole('button', { name: 'Settings' }).click();
  await page.screenshot({ path: '/data00/home/wangyang.49/codex-cpa-bridge/.qa/settings.png', fullPage: true });

  await browser.close();
})().catch((error) => {
  console.error(error);
  process.exit(1);
});
