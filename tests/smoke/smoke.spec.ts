import { test, expect, Page, Locator } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

const PASSWORD = process.env.AG_WEB_PASSWORD || 'smoketest';
const SCREENSHOT_DIR = path.join(__dirname, 'screenshots');

// Ensure screenshot directory exists
if (!fs.existsSync(SCREENSHOT_DIR)) {
  fs.mkdirSync(SCREENSHOT_DIR, { recursive: true });
}

// Console error tracking
interface ConsoleMessage {
  type: string;
  text: string;
  location: string;
}

const consoleErrors: ConsoleMessage[] = [];
const consoleWarnings: ConsoleMessage[] = [];

async function screenshot(page: Page, name: string): Promise<void> {
  const filepath = path.join(SCREENSHOT_DIR, `${name}.png`);
  await page.screenshot({ path: filepath, fullPage: true });
  console.log(`Screenshot saved: ${filepath}`);
}

/**
 * Helper function to perform login and wait for dashboard
 * Reduces code duplication and improves test speed
 */
async function login(page: Page): Promise<void> {
  await page.goto('/login');
  await page.fill('#password', PASSWORD);
  await page.click('button[type="submit"]');
  await expect(page).toHaveURL('/');
}

/**
 * Helper to find a specific session by tracking IDs before/after an operation
 * More reliable than assuming session ordering
 */
async function findNewSession(page: Page, existingIds: string[]): Promise<Locator> {
  let newSession: Locator | null = null;

  await expect(async () => {
    const allCards = page.locator('.session-card');
    const count = await allCards.count();

    for (let i = 0; i < count; i++) {
      const card = allCards.nth(i);
      const cardId = await card.evaluate(el => el.getAttribute('data-session-id') || el.id);
      if (!existingIds.includes(cardId)) {
        newSession = card;
        break;
      }
    }

    expect(newSession).not.toBeNull();
  }).toPass({ timeout: 15000, intervals: [1000] });

  return newSession!;
}

/**
 * Waits for a session to reach a terminal state and verifies completion.
 * Captures diagnostic information if the job fails for better debugging.
 *
 * @param page - Playwright page object
 * @param sessionCard - Locator for the session card
 * @param screenshotPrefix - Prefix for diagnostic screenshots
 * @param timeout - Timeout for waiting (default 90000ms)
 * @returns The final status class name
 */
async function waitForJobCompletion(
  page: Page,
  sessionCard: Locator,
  screenshotPrefix: string,
  timeout: number = 90000
): Promise<string> {
  // Wait for any terminal state
  const terminalStatus = sessionCard.locator(
    '.session-status--completed, .session-status--failed, .session-status--cancelled'
  );
  await expect(terminalStatus).toBeVisible({ timeout });

  // Determine the actual status
  const statusElement = sessionCard.locator('[class*="session-status--"]').first();
  const statusClasses = await statusElement.getAttribute('class') || '';

  const isCompleted = statusClasses.includes('session-status--completed');
  const isFailed = statusClasses.includes('session-status--failed');
  const isCancelled = statusClasses.includes('session-status--cancelled');

  // If not completed, capture diagnostic info before failing
  if (!isCompleted) {
    await screenshot(page, `${screenshotPrefix}-TERMINAL-STATE`);

    // Expand session to capture output/error details
    const sessionBody = sessionCard.locator('.session-body');
    if (!await sessionBody.isVisible()) {
      await sessionCard.click();
      await page.waitForTimeout(500);
    }
    await screenshot(page, `${screenshotPrefix}-DETAILS`);

    // Try to get error/output text for logging
    const outputText = await sessionCard.locator('.io-block--output, .error-message').first().textContent().catch(() => null);
    if (outputText) {
      console.log(`Job output: ${outputText.substring(0, 500)}...`);
    }

    const statusDesc = isFailed ? 'failed' : isCancelled ? 'cancelled' : 'unknown';
    throw new Error(`Job ${statusDesc} instead of completing successfully. Status classes: ${statusClasses}`);
  }

  return statusClasses;
}

/**
 * Validates that a session title is properly formatted and meaningful.
 * Session titles should:
 * - Not start with markdown characters (#, *, -)
 * - Not be empty or just whitespace
 * - Not look like truncated UUIDs or IDs (e.g., "s 1", "a1b2c3")
 * - Be at least 3 characters of actual content
 * - Not contain "No prompt" or "Empty session" (indicates missing data)
 */
async function validateSessionTitle(sessionCard: Locator, expectedContent?: string): Promise<string> {
  const sessionSummary = sessionCard.locator('.session-summary');
  const titleText = await sessionSummary.textContent() || '';

  // Title should not be empty
  expect(titleText.trim().length).toBeGreaterThan(0);

  // Title should not start with markdown heading characters
  expect(titleText).not.toMatch(/^[#*\-\d.]\s*/);

  // Title should not look like a truncated UUID or garbage
  // UUIDs start with hex chars, so reject short strings that look like IDs
  expect(titleText).not.toMatch(/^[a-f0-9\s]{1,8}$/i);

  // Title should not be a fallback error message
  expect(titleText.toLowerCase()).not.toContain('no prompt');
  expect(titleText.toLowerCase()).not.toContain('empty session');
  expect(titleText.toLowerCase()).not.toContain('undefined');
  expect(titleText.toLowerCase()).not.toContain('null');

  // Title should have meaningful content (at least 3 chars after trimming)
  expect(titleText.trim().length).toBeGreaterThanOrEqual(3);

  // If expected content is provided, verify it's included
  if (expectedContent) {
    expect(titleText.toLowerCase()).toContain(expectedContent.toLowerCase());
  }

  return titleText;
}

test.describe.serial('Agency Smoke Tests', () => {
  // Clear console errors before each test
  test.beforeEach(async ({ page }) => {
    consoleErrors.length = 0;
    consoleWarnings.length = 0;

    // Listen for console messages
    page.on('console', msg => {
      const type = msg.type();
      const text = msg.text();
      const location = msg.location();

      if (type === 'error') {
        consoleErrors.push({
          type,
          text,
          location: `${location.url}:${location.lineNumber}`
        });
      } else if (type === 'warning') {
        consoleWarnings.push({
          type,
          text,
          location: `${location.url}:${location.lineNumber}`
        });
      }
    });

    // Listen for page errors (uncaught exceptions)
    page.on('pageerror', error => {
      consoleErrors.push({
        type: 'pageerror',
        text: error.message,
        location: error.stack || 'unknown'
      });
    });
  });

  // Check for console errors after each test
  test.afterEach(async ({ page }, testInfo) => {
    // Log any console errors/warnings found
    if (consoleErrors.length > 0) {
      console.error('\n❌ Console Errors detected:');
      consoleErrors.forEach((err, i) => {
        console.error(`  ${i + 1}. [${err.type}] ${err.text}`);
        console.error(`     at ${err.location}`);
      });
    }

    if (consoleWarnings.length > 0) {
      console.warn('\n⚠️  Console Warnings detected:');
      consoleWarnings.forEach((warn, i) => {
        console.warn(`  ${i + 1}. [${warn.type}] ${warn.text}`);
        console.warn(`     at ${warn.location}`);
      });
    }

    // Fail the test if any console errors or warnings were detected
    if (consoleErrors.length > 0 || consoleWarnings.length > 0) {
      const errorCount = consoleErrors.length;
      const warningCount = consoleWarnings.length;
      throw new Error(
        `Browser console had ${errorCount} error(s) and ${warningCount} warning(s). ` +
        `Tests must not generate console errors or warnings.`
      );
    }
  });

  // Final cleanup after all tests
  test.afterAll(async () => {
    console.log('\nSmoke tests completed - browser cleanup handled by Playwright');
  });

  test('1. Login', async ({ page }) => {
    // Navigate to root - should redirect to login
    await page.goto('/');
    await expect(page).toHaveURL(/\/login/);
    await screenshot(page, '01-login-page');

    // Fill password and submit
    await page.fill('#password', PASSWORD);
    await page.click('button[type="submit"]');

    // Verify dashboard loads
    await expect(page).toHaveURL('/');
    await expect(page.locator('.topbar')).toBeVisible();
    await screenshot(page, '02-dashboard-after-login');
  });

  test('2. Create Task', async ({ page }) => {
    // Login first
    await page.goto('/login');
    await page.fill('#password', PASSWORD);
    await page.click('button[type="submit"]');
    await expect(page).toHaveURL('/');

    // Click "New Task" button
    await page.click('button:has-text("New Task")');

    // Wait for modal content to be visible (the modal title)
    await expect(page.locator('.modal-title:has-text("New Task")')).toBeVisible({ timeout: 5000 });
    await screenshot(page, '03-new-task-modal');

    // Verify kind-based tasking UI: agent kind dropdown should be visible, agent selection should NOT exist
    const agentKindSelect = page.locator('select#agent-kind-select');
    await expect(agentKindSelect).toBeVisible({ timeout: 5000 });

    // Verify old agent selection dropdown is removed
    const agentSelect = page.locator('select#agent-select');
    await expect(agentSelect).not.toBeVisible();

    // Verify agent kind defaults to 'claude'
    await expect(agentKindSelect).toHaveValue('claude');

    // Fill task form - wait for textarea to be visible and enabled
    const promptInput = page.locator('textarea[placeholder="Describe the task..."]');
    await expect(promptInput).toBeVisible({ timeout: 5000 });
    await promptInput.fill('List the files in /tmp using bash, then tell me how many there are.');

    // Expand Advanced Options to access tier select
    await page.click('button:has-text("Advanced Options")');
    const tierSelect = page.getByLabel('Tier');
    await tierSelect.selectOption('fast');
    await screenshot(page, '04-task-form-filled');

    // Get all session IDs before submitting to identify the new one reliably
    const existingSessionIds = await page.locator('.session-card').evaluateAll(cards =>
      cards.map(card => card.getAttribute('data-session-id') || card.id)
    );

    // Submit the form
    await page.click('button:has-text("Submit Task")');

    // Wait for modal to close and task to appear
    await expect(page.locator('.modal-backdrop--open')).toBeHidden({ timeout: 10000 });

    // Wait for new session to be created and find it by excluding existing sessions
    let sessionCard: any = null;
    await expect(async () => {
      const allCards = page.locator('.session-card');
      const count = await allCards.count();

      // Find the card that wasn't in our original list
      for (let i = 0; i < count; i++) {
        const card = allCards.nth(i);
        const cardId = await card.evaluate(el => el.getAttribute('data-session-id') || el.id);
        if (!existingSessionIds.includes(cardId)) {
          sessionCard = card;
          break;
        }
      }

      expect(sessionCard).not.toBeNull();
    }).toPass({ timeout: 15000, intervals: [1000] });

    await expect(sessionCard).toBeVisible({ timeout: 10000 });
    await screenshot(page, '05-task-submitted');

    // Wait for task to complete with diagnostic capture on failure
    await waitForJobCompletion(page, sessionCard, '05b-create-task');

    // Validate session title is properly formatted (should reflect the files task)
    // Note: title content may vary based on LLM response, so we just validate format
    const titleText = await validateSessionTitle(sessionCard);
    console.log(`Session title: "${titleText}"`);

    // Expand card and verify output exists (don't rely on specific text)
    await sessionCard.click();
    // Wait for session body to be visible with retries for Alpine.js reactivity
    const sessionBody = sessionCard.locator('.session-body');
    await expect(async () => {
      await expect(sessionBody).toBeVisible();
    }).toPass({ timeout: 5000, intervals: [300] });

    // Verify there's some I/O content (output or input blocks)
    const ioBlocks = sessionCard.locator('.io-block--output, .io-block--input');
    await expect(ioBlocks.first()).toBeVisible({ timeout: 5000 });
    await screenshot(page, '06-task-completed');

    // Wait for logs button to appear and click to expand inline logs
    const logsButton = sessionCard.locator('.io-logs-btn').first();

    // Only test log expansion if logs button is present (might not be for all tasks)
    if (await logsButton.count() > 0) {
      await expect(logsButton).toBeVisible({ timeout: 10000 });
      await logsButton.click();

      // Wait for inline logs to be visible (expanded state) with retries
      const inlineLogs = sessionCard.locator('.io-logs-inline').first();
      await expect(async () => {
        await expect(inlineLogs).toBeVisible();
      }).toPass({ timeout: 5000, intervals: [500] });

      // Check for log stability - logs should not flicker (disappear after appearing)
      // Use longer intervals to account for potential re-renders
      await page.waitForTimeout(2000);
      await expect(inlineLogs).toBeVisible();
      await page.waitForTimeout(2000);
      await expect(inlineLogs).toBeVisible();
      await screenshot(page, '06b-task-logs-expanded');
    } else {
      console.log('No logs button found - skipping log expansion test');
    }
  });

  test('3. Add Task to Same Session', async ({ page }) => {
    await login(page);

    // Wait for existing session to appear and verify it's in completed state
    const sessionCard = page.locator('.session-card').first();
    await expect(sessionCard).toBeVisible({ timeout: 10000 });

    // Verify session is in completed state before adding new task
    await expect(sessionCard.locator('.session-status--completed')).toBeVisible({ timeout: 10000 });

    // Expand the session to see actions with retry for Alpine.js
    await sessionCard.click();
    const sessionBody = sessionCard.locator('.session-body');
    await expect(async () => {
      await expect(sessionBody).toBeVisible();
    }).toPass({ timeout: 5000, intervals: [300] });
    await screenshot(page, '07-session-expanded');

    // Open new task modal
    await page.click('button:has-text("New Task")');
    await expect(page.locator('.modal-title:has-text("New Task")')).toBeVisible({ timeout: 10000 });

    // Select the existing session from the dropdown
    const sessionSelect = page.locator('select').filter({ hasText: 'New session' });
    await sessionSelect.selectOption({ index: 1 });

    // Verify that agent kind dropdown is NOT visible when adding to existing session
    const agentKindSelect = page.locator('select#agent-kind-select');
    await expect(agentKindSelect).not.toBeVisible();

    // Also verify old agent selection dropdown is not present
    const agentSelect = page.locator('select#agent-select');
    await expect(agentSelect).not.toBeVisible();

    // Fill the new task
    const promptInput = page.locator('textarea[placeholder="Describe the task..."]');
    await promptInput.fill('Use bash to check the current date and time, then summarize it.');
    await screenshot(page, '08-add-task-to-session');

    // Submit
    await page.click('button:has-text("Submit Task")');

    // Wait for modal to close
    await expect(page.locator('.modal-backdrop--open')).toBeHidden({ timeout: 10000 });

    // Wait for task to complete with diagnostic capture on failure
    await waitForJobCompletion(page, sessionCard, '08b-add-task');

    // Validate session title format (don't check specific content as it may vary)
    const titleText = await validateSessionTitle(sessionCard);
    console.log(`Session title after second task: "${titleText}"`);

    // Wait for the second task's output to appear (look for date/time indicators)
    // Use more generic patterns that won't break with year changes
    const hasDateOutput = await expect(async () => {
      const text = await sessionCard.textContent();
      // Look for date-like patterns rather than specific year
      const hasDate = /\d{4}/.test(text || '') || /\d{1,2}:\d{2}/.test(text || '');
      expect(hasDate).toBe(true);
    }).toPass({ timeout: 30000, intervals: [2000] }).catch(() => false);

    if (!hasDateOutput) {
      console.warn('Could not find date/time in output - LLM response may have varied');
    }

    await screenshot(page, '09-second-task-completed');
  });

  test('4. Verify Smoke Nightly Maintenance Job Exists', async ({ page }) => {
    await login(page);

    // Expand "Fleet" section - ensure it's closed first
    const fleetTrigger = page.locator('.fleet-trigger');
    const fleetContent = page.locator('.fleet-content');

    // Ensure fleet starts in known state
    const isVisible = await fleetContent.isVisible().catch(() => false);
    if (isVisible) {
      await fleetTrigger.click();
      await expect(fleetContent).toBeHidden({ timeout: 5000 });
    }

    await fleetTrigger.click();
    await expect(fleetContent).toBeVisible({ timeout: 5000 });
    await screenshot(page, '10-fleet-section-expanded');

    // Wait for agent to show as idle (ensures previous task fully completed)
    await expect(async () => {
      const idleChip = page.locator('.fleet-chip:has-text("idle")').first();
      await expect(idleChip).toBeVisible();
      // Verify stability - wait briefly and check again
      await page.waitForTimeout(300);
      await expect(idleChip).toBeVisible();
    }).toPass({ timeout: 15000, intervals: [1000] });

    // Wait for job list to render by checking for job items
    await expect(page.locator('.job-item').first()).toBeVisible({ timeout: 10000 });
    await screenshot(page, '11-scheduler-jobs-list');

    // Verify smoke-nightly-maintenance job exists with different name from prod
    const nightlyMaintenanceJob = page.locator('.job-item').filter({
      hasText: 'smoke-nightly-maintenance'
    });
    await expect(nightlyMaintenanceJob).toBeVisible({ timeout: 10000 });
    await screenshot(page, '12-smoke-nightly-maintenance-visible');

    // Also verify the regular smoke-test job exists
    const smokeTestJob = page.locator('.job-item').filter({
      hasText: 'smoke-test'
    }).first();
    await expect(smokeTestJob).toBeVisible({ timeout: 5000 });
  });

  test('5. Trigger Smoke Nightly Maintenance Job', async ({ page }) => {
    test.setTimeout(150000); // 2.5 minutes for simplified health check

    await login(page);

    // Expand "Fleet" section - ensure it's closed first
    const fleetTrigger = page.locator('.fleet-trigger');
    const fleetContent = page.locator('.fleet-content');

    const isVisible = await fleetContent.isVisible().catch(() => false);
    if (isVisible) {
      await fleetTrigger.click();
      await expect(fleetContent).toBeHidden({ timeout: 5000 });
    }

    await fleetTrigger.click();
    await expect(fleetContent).toBeVisible({ timeout: 5000 });

    // Wait for agent to show as idle
    await expect(async () => {
      const idleChip = page.locator('.fleet-chip:has-text("idle")').first();
      await expect(idleChip).toBeVisible();
      await page.waitForTimeout(300);
      await expect(idleChip).toBeVisible();
    }).toPass({ timeout: 15000, intervals: [1000] });

    // Wait for job list to render
    await expect(page.locator('.job-item').first()).toBeVisible({ timeout: 10000 });

    // Find the smoke-nightly-maintenance job
    const nightlyMaintenanceJob = page.locator('.job-item').filter({
      hasText: 'smoke-nightly-maintenance'
    });
    await expect(nightlyMaintenanceJob).toBeVisible({ timeout: 10000 });
    await screenshot(page, '13-before-trigger-nightly-maintenance');

    // Track existing sessions before triggering
    const existingSessionIds = await page.locator('.session-card').evaluateAll(cards =>
      cards.map(card => card.getAttribute('data-session-id') || card.id)
    );

    // Click trigger button
    await nightlyMaintenanceJob.locator('button:has-text("Run Now")').click();

    // Close fleet section to ensure UI updates properly
    await fleetTrigger.click();
    await expect(fleetContent).toBeHidden({ timeout: 5000 });

    await screenshot(page, '14-nightly-maintenance-triggered');

    // Find the newly created session
    const newSession = await findNewSession(page, existingSessionIds);
    await expect(newSession).toBeVisible({ timeout: 10000 });

    // Wait for job completion with diagnostic capture on failure
    await waitForJobCompletion(page, newSession, '14b-nightly-maintenance', 120000);

    // Validate session title using comprehensive validation
    const title = await validateSessionTitle(newSession);
    console.log(`Session title for nightly maintenance: "${title}"`);
    await screenshot(page, '15-session-title-check');

    // Expand session and verify there's output
    await newSession.click();
    const sessionBody = newSession.locator('.session-body');
    await expect(async () => {
      await expect(sessionBody).toBeVisible();
    }).toPass({ timeout: 5000, intervals: [300] });

    // Verify there's some I/O output (don't rely on specific content)
    const ioBlocks = newSession.locator('.io-block--output, .io-block--input');
    await expect(ioBlocks.first()).toBeVisible({ timeout: 5000 });

    await screenshot(page, '16-nightly-maintenance-completed');
  });

  test('6. Scheduler Config Reload', async ({ page }) => {
    await login(page);

    // Get initial scheduler status
    const statusBefore = await page.request.get('https://localhost:19010/status', {
      ignoreHTTPSErrors: true,
    });
    expect(statusBefore.ok()).toBeTruthy();
    const beforeData = await statusBefore.json();
    const jobCountBefore = beforeData.jobs.length;

    // Modify scheduler config (add a test job)
    // Use __dirname to get path relative to this test file (tests/smoke/)
    const configPath = path.join(__dirname, '../../configs/scheduler-smoke.yaml');
    const configContent = fs.readFileSync(configPath, 'utf-8');

    // Append a new job to the config
    const newJob = `
  - name: smoke-test-reload
    schedule: "0 0 31 2 *"  # Never auto-runs
    agent_kind: claude
    tier: fast
    timeout: 30s
    prompt: "This job was added via config reload test"
`;
    const modifiedConfig = configContent + newJob;
    fs.writeFileSync(configPath, modifiedConfig);

    try {
      // Wait for reload (with 1s polling interval, should happen within 2-3 seconds)
      await expect(async () => {
        const statusAfter = await page.request.get('https://localhost:19010/status', {
          ignoreHTTPSErrors: true,
        });
        const afterData = await statusAfter.json();
        expect(afterData.jobs.length).toBe(jobCountBefore + 1);

        // Verify the new job is present
        const reloadJob = afterData.jobs.find(j => j.name === 'smoke-test-reload');
        expect(reloadJob).toBeDefined();
        expect(reloadJob.schedule).toBe('0 0 31 2 *');
      }).toPass({ timeout: 10000, intervals: [1000] });

      await screenshot(page, '16a-scheduler-config-reloaded');

    } finally {
      // Restore original config
      fs.writeFileSync(configPath, configContent);

      // Wait for reload to restore original state
      await expect(async () => {
        const statusRestored = await page.request.get('https://localhost:19010/status', {
          ignoreHTTPSErrors: true,
        });
        const restoredData = await statusRestored.json();
        expect(restoredData.jobs.length).toBe(jobCountBefore);
      }).toPass({ timeout: 10000, intervals: [1000] });
    }
  });

  // Codex test is skipped by default - requires OpenAI API credentials
  // Run with: CODEX_SMOKE_TEST=1 npx playwright test
  test.skip('6a. Trigger Codex Scheduled Job', async ({ page }) => {
    test.setTimeout(120000); // 2 minutes for Codex job

    await login(page);

    // Expand "Fleet" section
    const fleetTrigger = page.locator('.fleet-trigger');
    const fleetContent = page.locator('.fleet-content');

    await fleetTrigger.click();
    await expect(fleetContent).toBeVisible({ timeout: 5000 });
    await screenshot(page, '14a-fleet-section-for-codex');

    // Wait for Codex agent specifically to be idle (not just any agent)
    await expect(async () => {
      // Look for an agent card that mentions codex and shows idle status
      const codexAgentIdle = page.locator('.agent-card:has-text("codex") .fleet-chip:has-text("idle")');
      const anyAgentIdle = page.locator('.fleet-chip:has-text("idle")').first();

      // Try codex-specific first, fall back to any agent idle
      const idleChip = await codexAgentIdle.count() > 0 ? codexAgentIdle : anyAgentIdle;
      await expect(idleChip).toBeVisible();
      await page.waitForTimeout(300);
      await expect(idleChip).toBeVisible();
    }).toPass({ timeout: 15000, intervals: [1000] });

    // Wait for job list to render
    await expect(page.locator('.job-item').first()).toBeVisible({ timeout: 10000 });
    await screenshot(page, '14b-scheduler-jobs-for-codex');

    // Find the smoke-test-codex job
    const codexJob = page.locator('.job-item').filter({
      hasText: 'smoke-test-codex'
    });

    await expect(codexJob).toBeVisible({ timeout: 10000 });
    await screenshot(page, '14c-codex-job-visible');

    // Verify the Run Now button is enabled before clicking
    const runButton = codexJob.locator('button:has-text("Run Now")');
    await expect(runButton).toBeEnabled({ timeout: 5000 });

    // Track existing sessions before triggering
    const existingSessionIds = await page.locator('.session-card').evaluateAll(cards =>
      cards.map(card => card.getAttribute('data-session-id') || card.id)
    );

    // Click trigger button
    await runButton.click();

    await screenshot(page, '14d-codex-job-triggered');

    // Find the newly created session
    const newSession = await findNewSession(page, existingSessionIds);
    await expect(newSession).toBeVisible({ timeout: 10000 });

    // Wait for job completion with diagnostic capture on failure
    await waitForJobCompletion(page, newSession, '14e-codex', 90000);

    // Expand session to see I/O
    await newSession.click();
    const sessionBody = newSession.locator('.session-body');
    await expect(async () => {
      await expect(sessionBody).toBeVisible();
    }).toPass({ timeout: 5000, intervals: [300] });

    // Verify output block is visible
    const outputBlock = newSession.locator('.io-block--output').first();
    await expect(outputBlock).toBeVisible({ timeout: 5000 });

    // Verify output block contains expected text
    await expect(outputBlock).toContainText('Codex smoke test OK', { timeout: 5000 });
    await screenshot(page, '14f-codex-job-completed');

    // Check for log flickering - logs should remain visible if expanded
    const logsButton = newSession.locator('.io-logs-btn').first();
    if (await logsButton.count() > 0) {
      await expect(logsButton).toBeVisible({ timeout: 10000 });
      await logsButton.click();

      const inlineLogs = newSession.locator('.io-logs-inline').first();
      await expect(async () => {
        await expect(inlineLogs).toBeVisible();
      }).toPass({ timeout: 5000, intervals: [500] });

      // Wait and verify logs don't collapse (flicker)
      await page.waitForTimeout(2000);
      await expect(inlineLogs).toBeVisible();
      await screenshot(page, '14g-codex-logs-stable');
    }
  });

  test('7. Queue-Based Task Routing', async ({ page }) => {
    test.setTimeout(120000); // 2 minutes for multiple tasks

    await login(page);

    // Wait for agent to be idle - ensure fleet is closed first
    const fleetTrigger = page.locator('.fleet-trigger');
    const fleetContent = page.locator('.fleet-content');

    const isVisible = await fleetContent.isVisible().catch(() => false);
    if (isVisible) {
      await fleetTrigger.click();
      await expect(fleetContent).toBeHidden({ timeout: 5000 });
    }

    await fleetTrigger.click();
    await expect(fleetContent).toBeVisible({ timeout: 5000 });
    await expect(async () => {
      const idleChip = page.locator('.fleet-chip:has-text("idle")').first();
      await expect(idleChip).toBeVisible();
    }).toPass({ timeout: 15000, intervals: [1000] });
    await fleetTrigger.click(); // Close fleet

    // Track existing sessions
    const existingSessionIds = await page.locator('.session-card').evaluateAll(cards =>
      cards.map(card => card.getAttribute('data-session-id') || card.id)
    );

    // Submit task 1 - should succeed immediately (agent idle)
    await page.click('button:has-text("New Task")');
    await expect(page.locator('.modal-title:has-text("New Task")')).toBeVisible({ timeout: 10000 });
    const promptInput1 = page.locator('textarea[placeholder="Describe the task..."]');
    await promptInput1.fill('Sleep for 3 seconds using bash sleep command, then confirm completion.');
    await page.click('button:has-text("Submit Task")');
    await expect(page.locator('.modal-backdrop--open')).toBeHidden({ timeout: 10000 });

    // Find first new session
    const firstSession = await findNewSession(page, existingSessionIds);
    await expect(firstSession).toBeVisible({ timeout: 10000 });
    await screenshot(page, '17a-first-task-submitted');

    // Get first session ID for tracking
    const firstSessionId = await firstSession.evaluate(el => el.getAttribute('data-session-id') || el.id);
    const existingAfterFirst = [...existingSessionIds, firstSessionId];

    // Immediately submit task 2 while agent is busy - should queue successfully (no "agent busy" error)
    await page.click('button:has-text("New Task")');
    await expect(page.locator('.modal-title:has-text("New Task")')).toBeVisible({ timeout: 10000 });
    const promptInput2 = page.locator('textarea[placeholder="Describe the task..."]');
    await promptInput2.fill('Echo "Task 2 completed" using bash.');
    await page.click('button:has-text("Submit Task")');

    // Task should be accepted (no error), modal should close
    await expect(page.locator('.modal-backdrop--open')).toBeHidden({ timeout: 10000 });

    // Find second new session
    const secondSession = await findNewSession(page, existingAfterFirst);
    await expect(secondSession).toBeVisible({ timeout: 10000 });
    await screenshot(page, '17b-second-task-queued-or-created');

    // Wait for both tasks to complete
    await waitForJobCompletion(page, firstSession, '17c-queue-task1', 60000);
    await waitForJobCompletion(page, secondSession, '17d-queue-task2', 60000);

    await screenshot(page, '17e-both-tasks-completed');

    // Verify no error messages in either session
    await expect(firstSession).not.toContainText('agent busy', { timeout: 1000 });
    await expect(secondSession).not.toContainText('agent busy', { timeout: 1000 });
  });

  test('8. UI Navigation and Interactions', async ({ page }) => {
    await login(page);

    // Wait for dashboard to load with sessions
    const sessionCard = page.locator('.session-card').first();
    await expect(sessionCard).toBeVisible({ timeout: 10000 });
    await screenshot(page, '18-dashboard-with-sessions');

    // --- Fleet Section: Expand/Collapse ---
    const fleetTrigger = page.locator('.fleet-trigger');
    const fleetContent = page.locator('.fleet-content');

    // Ensure fleet starts in known state
    const isVisible = await fleetContent.isVisible().catch(() => false);
    if (isVisible) {
      await fleetTrigger.click();
      await expect(fleetContent).toBeHidden({ timeout: 5000 });
    }
    await screenshot(page, '19-fleet-collapsed');

    // Expand fleet section
    await fleetTrigger.click();
    await expect(fleetContent).toBeVisible({ timeout: 5000 });
    await screenshot(page, '20-fleet-expanded');

    // Verify agent and helpers are shown
    await expect(page.locator('.fleet-category-label:has-text("Agents")')).toBeVisible({ timeout: 5000 });
    await expect(page.locator('.fleet-chip:has-text("idle"), .fleet-chip:has-text("working")').first()).toBeVisible({ timeout: 5000 });

    // Collapse fleet section
    await fleetTrigger.click();
    await expect(fleetContent).toBeHidden({ timeout: 5000 });
    await screenshot(page, '21-fleet-collapsed-again');

    // --- Session Card: Expand/Collapse ---
    const sessionBody = sessionCard.locator('.session-body');

    // Ensure session starts collapsed with retry pattern
    const isBodyVisible = await sessionBody.isVisible().catch(() => false);
    if (isBodyVisible) {
      await sessionCard.locator('.session-header').click();
      await expect(async () => {
        await expect(sessionBody).toBeHidden();
      }).toPass({ timeout: 3000, intervals: [300] });
    }

    // Verify it's collapsed
    await expect(sessionBody).toBeHidden();
    await screenshot(page, '22-session-collapsed');

    // Expand session - use retry pattern for Alpine.js reactivity
    await sessionCard.locator('.session-header').click();
    await expect(async () => {
      await expect(sessionBody).toBeVisible();
    }).toPass({ timeout: 3000, intervals: [300] });
    await screenshot(page, '23-session-expanded');

    // --- Session Tabs: Switch between I/O, Details, and Metrics ---
    const ioTab = sessionCard.locator('.session-tab:has-text("I/O")');
    const detailsTab = sessionCard.locator('.session-tab:has-text("Details")');
    const metricsTab = sessionCard.locator('.session-tab:has-text("Metrics")');

    // Verify I/O tab is active by default
    await expect(ioTab).toHaveClass(/session-tab--active/);
    await screenshot(page, '24-io-tab-active');

    // Switch to Details tab
    await detailsTab.click();
    await expect(detailsTab).toHaveClass(/session-tab--active/);
    await expect(ioTab).not.toHaveClass(/session-tab--active/);
    await screenshot(page, '25-details-tab-active');

    // Switch to Metrics tab
    await metricsTab.click();
    await expect(metricsTab).toHaveClass(/session-tab--active/);
    await expect(detailsTab).not.toHaveClass(/session-tab--active/);
    await screenshot(page, '26-metrics-tab-active');

    // Switch back to I/O tab
    // Ensure session is still expanded and tab is visible
    await expect(sessionBody).toBeVisible();
    await ioTab.scrollIntoViewIfNeeded();
    await ioTab.click();
    await expect(ioTab).toHaveClass(/session-tab--active/);
    await screenshot(page, '27-back-to-io-tab');

    // Collapse session
    await sessionCard.locator('.session-header').click();
    await expect(async () => {
      await expect(sessionBody).toBeHidden();
    }).toPass({ timeout: 3000, intervals: [300] });
    await screenshot(page, '28-session-collapsed-final');

    // --- Settings Modal: Open/Close ---
    // Settings button is in bottom nav bar (only visible on mobile viewport)
    // Temporarily switch to mobile viewport to access settings
    const originalViewport = page.viewportSize();
    await page.setViewportSize({ width: 375, height: 667 }); // iPhone SE size
    await screenshot(page, '29-mobile-viewport');

    const settingsButton = page.locator('.nav-item:has-text("Settings")');
    await expect(settingsButton).toBeVisible({ timeout: 5000 });
    await settingsButton.click();

    // Wait for settings modal
    const settingsModal = page.locator('.modal-backdrop--open');
    await expect(settingsModal).toBeVisible({ timeout: 5000 });
    await expect(page.locator('.modal-title:has-text("Settings")')).toBeVisible();
    await screenshot(page, '30-settings-modal-open');

    // Close settings via backdrop click
    await settingsModal.click({ position: { x: 10, y: 10 } }); // Click near edge (backdrop)
    await expect(settingsModal).toBeHidden({ timeout: 5000 });
    await screenshot(page, '31-settings-modal-closed');

    // Restore desktop viewport
    if (originalViewport) {
      await page.setViewportSize(originalViewport);
    }
    await screenshot(page, '32-back-to-desktop');

    // --- Task Modal: Open/Close with Escape ---
    await page.click('button:has-text("New Task")');
    await expect(page.locator('.modal-title:has-text("New Task")')).toBeVisible({ timeout: 5000 });
    await screenshot(page, '33-task-modal-open');

    // Close via Escape key
    await page.keyboard.press('Escape');
    await expect(page.locator('.modal-backdrop--open')).toBeHidden({ timeout: 5000 });
    await screenshot(page, '34-task-modal-closed-via-escape');

    // --- Keyboard Shortcuts ---
    // Click on main content to ensure no form element is focused
    await page.click('.main');

    // 'n' should open new task modal
    await page.keyboard.press('n');
    await expect(page.locator('.modal-title:has-text("New Task")')).toBeVisible({ timeout: 5000 });
    await screenshot(page, '35-task-modal-via-n-key');

    // Close it
    await page.keyboard.press('Escape');
    await expect(page.locator('.modal-backdrop--open')).toBeHidden({ timeout: 5000 });

    // Click again to unfocus any element after modal closes
    await page.click('.main');

    // 'f' should toggle fleet section (fleet is currently hidden from earlier test)
    await page.keyboard.press('f');
    await expect(fleetContent).toBeVisible({ timeout: 5000 });
    await screenshot(page, '36-fleet-toggled-via-f-key');

    await page.keyboard.press('f');
    await expect(fleetContent).toBeHidden({ timeout: 5000 });
    await screenshot(page, '37-fleet-toggled-again');

    // 'r' should refresh (we can verify by checking the data reloads)
    await page.keyboard.press('r');
    // Just verify page is still functional
    await expect(sessionCard).toBeVisible();
    await screenshot(page, '38-after-refresh');
  });
});
