import { expect, test, type Page } from '@playwright/test';
import path from 'node:path';

import {
  authToken,
  claudeContentOTLPLogs,
  claudeMCPConnectionOTLPLogs,
  claudeOutcomeSuccessOTLPLogs,
  claudePRLinkOTLPTraces,
  claudeRiskyAccessOTLPLogs,
  claudeSkillOTLPLogs,
  claudeToolSpanFilePathOTLPTraces,
  codexOutcomeOTLPLogs,
  expectFiveDestinationPrimaryNav,
  expectSessionTraceLanes,
  expectUtilityDestinations,
  ingestOTLPLogs,
  ingestOTLPTraces,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

const evidenceDir = path.resolve(process.cwd(), '../docs/ui/evidence/195');
const referenceSessionID = 'tiq-reference';
const referenceTimestamps = [
  '2026-09-11T09:06:40Z',
  '2026-09-11T09:06:41Z',
  '2026-09-11T09:06:42Z',
];
const viewports = [
  { name: '1440x900', width: 1440, height: 900 },
  { name: '1024x768', width: 1024, height: 768 },
  { name: '390x844', width: 390, height: 844 },
] as const;

type Destination = {
  name: 'overview' | 'sessions' | 'pull-requests' | 'models' | 'governance';
  path: string;
  heading: string | RegExp;
  prepare?: (page: Page) => Promise<void>;
};

const destinations: Destination[] = [
  {
    name: 'overview',
    path: '/',
    heading: 'Orchestration overview',
  },
  {
    name: 'sessions',
    path: `/sessions/${encodeURIComponent(`claude-code:${referenceSessionID}`)}`,
    heading: /^Session /,
    prepare: async (page) => {
      await expectSessionTraceLanes(page);
      await page.getByLabel('Conversation lane').getByRole('link').last().click();
      await expect(page.locator('#event-inspector')).toBeVisible();
    },
  },
  {
    name: 'pull-requests',
    path: '/pull-requests',
    heading: 'Pull Requests',
  },
  {
    name: 'models',
    path: '/models',
    heading: 'Models',
  },
  {
    name: 'governance',
    path: '/governance?rules=paths',
    heading: 'Governance Policies',
  },
];

resetDaemonBetweenTests();

async function seedReferenceScenario(page: Page): Promise<void> {
  await ingestOTLPLogs(claudeContentOTLPLogs(referenceSessionID, referenceTimestamps));
  await ingestOTLPTraces(claudeToolSpanFilePathOTLPTraces(referenceSessionID));
  await ingestOTLPLogs(claudeMCPConnectionOTLPLogs('tiq-reference-mcp'));
  await ingestOTLPLogs(claudeSkillOTLPLogs());
  await ingestOTLPLogs(claudeRiskyAccessOTLPLogs());
  await ingestOTLPTraces(claudePRLinkOTLPTraces());
  await ingestOTLPLogs(claudeOutcomeSuccessOTLPLogs());
  await ingestOTLPLogs(codexOutcomeOTLPLogs());
  await unlockDashboard(page, authToken);
}

async function openMobileNavigation(page: Page, width: number): Promise<void> {
  if (width >= 768) return;
  const menu = page.getByRole('button', { name: 'Menu' });
  await expect(menu).toBeVisible();
  if ((await menu.getAttribute('aria-expanded')) !== 'true') await menu.click();
  await expect(menu).toHaveAttribute('aria-expanded', 'true');
}

async function captureDestination(
  page: Page,
  destination: Destination,
  viewport: (typeof viewports)[number],
): Promise<void> {
  await page.setViewportSize({ width: viewport.width, height: viewport.height });
  await page.goto(destination.path);
  await destination.prepare?.(page);
  await openMobileNavigation(page, viewport.width);
  await expectFiveDestinationPrimaryNav(page);
  await expectUtilityDestinations(page, {
    costs: destination.name !== 'overview',
  });
  await expect(
    page.getByRole('heading', { name: destination.heading, level: 1 }),
  ).toBeVisible();
  await expect(page).toHaveScreenshot(
    `${destination.name}-${viewport.name}.png`,
    { animations: 'disabled' },
  );
  await page.screenshot({
    path: path.join(evidenceDir, `${destination.name}-${viewport.name}.png`),
    animations: 'disabled',
  });
}

function contrastRatio(first: number[], second: number[]): number {
  const luminance = (rgb: number[]): number => {
    const linear = rgb.map((channel) => {
      const value = channel / 255;
      return value <= 0.04045
        ? value / 12.92
        : ((value + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
  };
  const [lighter, darker] = [luminance(first), luminance(second)].sort(
    (left, right) => right - left,
  );
  return (lighter + 0.05) / (darker + 0.05);
}

function rgb(value: string): number[] {
  const hex = value.trim().replace('#', '');
  if (/^[\da-f]{6}$/i.test(hex)) {
    return [0, 2, 4].map((offset) => Number.parseInt(hex.slice(offset, offset + 2), 16));
  }
  return value.match(/\d+(?:\.\d+)?/g)?.map(Number) ?? [];
}

test('records deterministic reference-alignment baselines from live evidence', async ({
  page,
}) => {
  await seedReferenceScenario(page);

  for (const viewport of viewports) {
    for (const destination of destinations) {
      await captureDestination(page, destination, viewport);
    }
  }

  for (const destination of destinations.filter((item) =>
    ['sessions', 'governance'].includes(item.name),
  )) {
    await captureDestination(page, destination, {
      name: '1680x945',
      width: 1680,
      height: 945,
    });
  }
});

test('keeps the reference shell keyboard-accessible at zoom and reduced motion', async ({
  page,
}) => {
  await seedReferenceScenario(page);
  await page.goto('/governance?rules=mcp');

  const mcpTab = page.getByRole('tab', { name: 'MCP servers' });
  await mcpTab.focus();
  await page.keyboard.press('ArrowRight');
  await expect(page).toHaveURL(/\/governance\?rules=skills$/);
  await expect(page.getByRole('tab', { name: 'Skills' })).toBeFocused();
  await page.keyboard.press('End');
  await expect(page).toHaveURL(/\/governance\?rules=prompts$/);
  await page.keyboard.press('Home');
  await expect(page).toHaveURL(/\/governance$/);
  await expect(mcpTab).toBeFocused();

  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.setViewportSize({ width: 390, height: 844 });
  await openMobileNavigation(page, 390);
  const client = await page.context().newCDPSession(page);
  await client.send('Emulation.setPageScaleFactor', { pageScaleFactor: 2 });
  const overflowAtZoom = await page.evaluate(() => {
    const root = document.documentElement;
    return {
      clientWidth: root.clientWidth,
      scrollWidth: root.scrollWidth,
    };
  });
  expect(
    overflowAtZoom.scrollWidth,
    '200% zoom must not create body horizontal overflow',
  ).toBe(overflowAtZoom.clientWidth);
  await client.send('Emulation.setPageScaleFactor', { pageScaleFactor: 1 });
  await client.detach();

  const colors = await page.evaluate(() => {
    const root = getComputedStyle(document.documentElement);
    return {
      text: root.getPropertyValue('--color-text'),
      muted: root.getPropertyValue('--color-muted'),
      background: root.getPropertyValue('--color-bg'),
      accent: root.getPropertyValue('--color-accent'),
    };
  });
  expect(contrastRatio(rgb(colors.text), rgb(colors.background))).toBeGreaterThanOrEqual(4.5);
  expect(contrastRatio(rgb(colors.muted), rgb(colors.background))).toBeGreaterThanOrEqual(4.5);
  expect(contrastRatio(rgb(colors.accent), rgb(colors.background))).toBeGreaterThanOrEqual(3);
});
