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
type Viewport = {
  name: string;
  width: number;
  height: number;
};

const viewports: readonly Viewport[] = [
  { name: '1440x900', width: 1440, height: 900 },
  { name: '1024x768', width: 1024, height: 768 },
  { name: '390x844', width: 390, height: 844 },
];

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
    path: '/governance',
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
  viewport: Viewport,
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
    {
      animations: 'disabled',
      // Playwright pins Chromium, but system-ui resolves to a different
      // system font on the supported Ubuntu runners. The 5% budget is
      // calibrated above the observed 4% glyph-rasterisation variance and
      // still rejects material layout or colour regressions.
      maxDiffPixelRatio: 0.05,
    },
  );
  await page.screenshot({
    path: path.join(evidenceDir, `${destination.name}-${viewport.name}.png`),
    animations: 'disabled',
  });

  if (viewport.name === '1680x945' && ['sessions', 'governance'].includes(destination.name)) {
    const geometry = await page.evaluate((destinationName) => {
      const width = (selector: string): number =>
        Math.round(document.querySelector(selector)?.getBoundingClientRect().width ?? 0);
      return {
        sidebar: width('.app-sidebar'),
        rail: width(destinationName === 'sessions' ? '.right-rail' : '.governance-preview-rail'),
        lanes: document.querySelectorAll('.trace-lane').length,
        cards: document.querySelectorAll('.governance-summary-card').length,
      };
    }, destination.name);
    expect(geometry.sidebar, 'reference sidebar is 200px').toBeGreaterThanOrEqual(190);
    expect(geometry.sidebar, 'reference sidebar is 200px').toBeLessThanOrEqual(210);
    expect(geometry.rail, 'reference right rail remains a dense desktop column').toBeGreaterThanOrEqual(280);
    expect(geometry.rail, 'reference right rail remains a dense desktop column').toBeLessThanOrEqual(360);
    if (destination.name === 'sessions') expect(geometry.lanes).toBe(5);
    if (destination.name === 'governance') expect(geometry.cards).toBe(4);
  }
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

type RenderedColourPair = {
  label: string;
  foreground: string;
  background: string;
  minimum: number;
};

async function renderedColourPairs(page: Page): Promise<RenderedColourPair[]> {
  return page.evaluate(() => {
    const backgroundFor = (element: Element): string => {
      for (let current: Element | null = element; current; current = current.parentElement) {
        const background = getComputedStyle(current).backgroundColor;
        if (background !== 'rgba(0, 0, 0, 0)') return background;
      }
      return getComputedStyle(document.documentElement).backgroundColor;
    };
    const pairs = [
      ['page heading', '.app-content h1', 4.5],
      ['muted sidebar navigation', '.nav-link:not(.active)', 4.5],
      ['selected access-rule tab', '.rules-tab.active', 3],
    ] as const;
    return pairs.map(([label, selector, minimum]) => {
      const element = document.querySelector(selector);
      if (!element) throw new Error(`Missing representative colour target: ${selector}`);
      return {
        label,
        foreground: getComputedStyle(element).color,
        background: backgroundFor(element),
        minimum,
      };
    });
  });
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
  const motion = await page.evaluate(() => {
    const milliseconds = (duration: string): number => {
      const value = Number.parseFloat(duration);
      return duration.endsWith('ms') ? value : value * 1000;
    };
    const root = getComputedStyle(document.documentElement);
    const body = getComputedStyle(document.body);
    return {
      animationDuration: milliseconds(body.animationDuration),
      scrollBehavior: root.scrollBehavior,
      transitionDuration: milliseconds(body.transitionDuration),
    };
  });
  expect(motion.animationDuration).toBeLessThanOrEqual(0.01);
  expect(motion.transitionDuration).toBeLessThanOrEqual(0.01);
  expect(motion.scrollBehavior).toBe('auto');

  // A 640px CSS viewport is the reflow equivalent of 200% zoom on a 1280px
  // desktop viewport; page scale only magnifies and does not test reflow.
  await page.setViewportSize({ width: 640, height: 900 });
  await openMobileNavigation(page, 640);
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

  for (const pair of await renderedColourPairs(page)) {
    expect(
      contrastRatio(rgb(pair.foreground), rgb(pair.background)),
      `${pair.label} must meet its rendered-colour contrast target`,
    ).toBeGreaterThanOrEqual(pair.minimum);
  }
});
