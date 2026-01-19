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

    // Fill task form - wait for textarea to be visible and enabled
    const promptInput = page.locator('textarea[placeholder="Describe the task..."]');
    await expect(promptInput).toBeVisible({ timeout: 5000 });
    await promptInput.fill('List the files in /tmp using bash, then tell me how many there are.');

    // Select Manual context to enable model selection
    const contextSelect = page.locator('select').filter({ hasText: 'Manual' }).first();
    await contextSelect.selectOption('manual');

    // Expand Advanced Options to access model select
    await page.click('button:has-text("Advanced Options")');
    const modelSelect = page.getByLabel('Model');
    await modelSelect.selectOption('haiku');
    await screenshot(page, '04-task-form-filled');

    // Get initial session count before submitting
    const initialSessionCount = await page.locator('.session-card').count();

    // Submit the form
    await page.click('button:has-text("Submit Task")');

    // Wait for modal to close and task to appear
    await expect(page.locator('.modal-backdrop--open')).toBeHidden({ timeout: 5000 });

    // Wait for new session to be created
    await expect(async () => {
      const newCount = await page.locator('.session-card').count();
      expect(newCount).toBeGreaterThan(initialSessionCount);
    }).toPass({ timeout: 10000, intervals: [1000] });

    // The newly created session should now be first
    const sessionCard = page.locator('.session-card').first();
    await expect(sessionCard).toBeVisible({ timeout: 10000 });
    await screenshot(page, '05-task-submitted');

    // Wait for task to reach a terminal state (completed, failed, or cancelled)
    const terminalStatus = sessionCard.locator('.session-status--completed, .session-status--failed, .session-status--cancelled');
    await expect(terminalStatus).toBeVisible({ timeout: 90000 });

    // Verify it completed successfully (not failed/cancelled)
    await expect(sessionCard.locator('.session-status--completed')).toBeVisible();

    // Validate session title is properly formatted (should reflect the files task)
    await validateSessionTitle(sessionCard, 'files');

    // Expand card and verify output contains evidence of file listing
    await sessionCard.click();
    await expect(sessionCard).toContainText('files', { timeout: 5000 });
    await screenshot(page, '06-task-completed');

    // Wait for logs button to appear and click to expand inline logs
    const logsButton = sessionCard.locator('.io-logs-btn').first();
    await expect(logsButton).toBeVisible({ timeout: 10000 });
    await logsButton.click();

    // Wait for inline logs to be visible (expanded state)
    const inlineLogs = sessionCard.locator('.io-logs-inline').first();
    await expect(inlineLogs).toBeVisible({ timeout: 5000 });

    // Check for log stability - logs should not flicker (disappear after appearing)
    await page.waitForTimeout(1000);
    await expect(inlineLogs).toBeVisible();
    await page.waitForTimeout(1000);
    await expect(inlineLogs).toBeVisible();
    await screenshot(page, '06b-task-logs-expanded');
  });

  test('3. Add Task to Same Session', async ({ page }) => {
    // Login first
    await page.goto('/login');
    await page.fill('#password', PASSWORD);
    await page.click('button[type="submit"]');
    await expect(page).toHaveURL('/');

    // Wait for existing session to appear
    const sessionCard = page.locator('.session-card').first();
    await expect(sessionCard).toBeVisible({ timeout: 10000 });

    // Expand the session to see actions
    await sessionCard.click();

    // Wait for the session body to be visible
    await expect(sessionCard.locator('.session-body')).toBeVisible({ timeout: 5000 });
    await screenshot(page, '07-session-expanded');

    // Use keyboard shortcut to open task modal (n key) for this session
    // Or click the global "+ New Task" button and select the session
    await page.click('button:has-text("New Task")');

    // Wait for modal
    await expect(page.locator('.modal-title:has-text("New Task")')).toBeVisible({ timeout: 5000 });

    // Select the existing session from the dropdown
    const sessionSelect = page.locator('select').filter({ hasText: 'New session' });
    // Get the session's first option that's not "New session"
    await sessionSelect.selectOption({ index: 1 });

    // Wait for modal to be visible
    await expect(page.locator('.modal-title:has-text("New Task")')).toBeVisible({ timeout: 5000 });

    // Fill the new task
    const promptInput = page.locator('textarea[placeholder="Describe the task..."]');
    await promptInput.fill('Use bash to check the current date and time, then summarize it.');
    await screenshot(page, '08-add-task-to-session');

    // Submit
    await page.click('button:has-text("Submit Task")');

    // Wait for modal to close
    await expect(page.locator('.modal-backdrop--open')).toBeHidden({ timeout: 5000 });

    // Wait for task to reach a terminal state
    const terminalStatus = sessionCard.locator('.session-status--completed, .session-status--failed, .session-status--cancelled');
    await expect(terminalStatus).toBeVisible({ timeout: 90000 });

    // Verify it completed successfully
    await expect(sessionCard.locator('.session-status--completed')).toBeVisible();

    // Validate session title still reflects the first task (files)
    await validateSessionTitle(sessionCard, 'files');

    // Wait for the second task's output to appear (date/time content)
    // The content should be visible from realtime updates even if history loading has issues
    await expect(sessionCard).toContainText('2026', { timeout: 30000 });
    await screenshot(page, '09-second-task-completed');
  });

  test('4. Verify Smoke Nightly Maintenance Job Exists', async ({ page }) => {
    // Login first
    await page.goto('/login');
    await page.fill('#password', PASSWORD);
    await page.click('button[type="submit"]');
    await expect(page).toHaveURL('/');

    // Expand "Fleet" section
    await page.click('.fleet-trigger');
    await expect(page.locator('.fleet-content')).toBeVisible();
    await screenshot(page, '10-fleet-section-expanded');

    // Wait for agent to show as idle (ensures previous task fully completed)
    await expect(async () => {
      const idleChip = page.locator('.fleet-chip:has-text("idle")').first();
      await expect(idleChip).toBeVisible();
      await page.waitForTimeout(500);
      await expect(idleChip).toBeVisible();
    }).toPass({ timeout: 10000, intervals: [1000] });

    // Wait for job list to render
    await page.waitForTimeout(1000);
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

    // Login first
    await page.goto('/login');
    await page.fill('#password', PASSWORD);
    await page.click('button[type="submit"]');
    await expect(page).toHaveURL('/');

    // Expand "Fleet" section
    await page.click('.fleet-trigger');
    await expect(page.locator('.fleet-content')).toBeVisible();

    // Wait for agent to show as idle (use first() to handle multiple agents)
    await expect(async () => {
      const idleChip = page.locator('.fleet-chip:has-text("idle")').first();
      await expect(idleChip).toBeVisible();
      await page.waitForTimeout(500);
      await expect(idleChip).toBeVisible();
    }).toPass({ timeout: 10000, intervals: [1000] });

    // Wait for job list to render
    await page.waitForTimeout(1000);

    // Find the smoke-nightly-maintenance job
    const nightlyMaintenanceJob = page.locator('.job-item').filter({
      hasText: 'smoke-nightly-maintenance'
    });
    await expect(nightlyMaintenanceJob).toBeVisible({ timeout: 10000 });
    await screenshot(page, '13-before-trigger-nightly-maintenance');

    // Get initial session count
    const initialSessionCount = await page.locator('.session-card').count();

    // Click trigger button
    await nightlyMaintenanceJob.locator('button:has-text("Run Now")').click();

    // Verify new session created
    await expect(async () => {
      const newCount = await page.locator('.session-card').count();
      expect(newCount).toBeGreaterThan(initialSessionCount);
    }).toPass({ timeout: 10000, intervals: [1000] });

    await screenshot(page, '14-nightly-maintenance-triggered');

    // Wait for job completion
    const newSession = page.locator('.session-card').first();
    const terminalStatus = newSession.locator('.session-status--completed, .session-status--failed, .session-status--cancelled');
    await expect(terminalStatus).toBeVisible({ timeout: 120000 });

    // Verify it completed successfully
    await expect(newSession.locator('.session-status--completed')).toBeVisible();

    // Validate session title using comprehensive validation
    // Should contain "Smoke Test Nightly Maintenance" (with markdown # stripped)
    const title = await validateSessionTitle(newSession, 'Smoke Test Nightly Maintenance');
    console.log(`Session title for nightly maintenance: "${title}"`);
    await screenshot(page, '15-session-title-check');

    // Verify output contains expected content related to helloworld2
    await newSession.click();
    // The job should mention helloworld2 in its output since that's the target repo
    await expect(newSession).toContainText('helloworld2', { timeout: 5000 });
    await screenshot(page, '16-nightly-maintenance-completed');
  });

  test('5. Trigger Codex Scheduled Job', async ({ page }) => {
    // Login first
    await page.goto('/login');
    await page.fill('#password', PASSWORD);
    await page.click('button[type="submit"]');
    await expect(page).toHaveURL('/');

    // Expand "Fleet" section
    await page.click('.fleet-trigger');
    await expect(page.locator('.fleet-content')).toBeVisible();
    await screenshot(page, '14a-fleet-section-for-codex');

    // Wait for agent to show as idle
    await expect(async () => {
      const idleChip = page.locator('.fleet-chip:has-text("idle")').first();
      await expect(idleChip).toBeVisible();
      await page.waitForTimeout(500);
      await expect(idleChip).toBeVisible();
    }).toPass({ timeout: 10000, intervals: [1000] });

    // Wait for job list to render
    await page.waitForTimeout(1000);
    await screenshot(page, '14b-scheduler-jobs-for-codex');

    // Find the smoke-test-codex job
    const codexJob = page.locator('.job-item').filter({
      hasText: 'smoke-test-codex'
    });

    await expect(codexJob).toBeVisible({ timeout: 10000 });
    await screenshot(page, '14c-codex-job-visible');

    // Get initial session count
    const initialSessionCount = await page.locator('.session-card').count();

    // Click trigger button
    await codexJob.locator('button:has-text("Run Now")').click();

    // Verify new session created
    await expect(async () => {
      const newCount = await page.locator('.session-card').count();
      expect(newCount).toBeGreaterThan(initialSessionCount);
    }).toPass({ timeout: 10000, intervals: [1000] });

    await screenshot(page, '14d-codex-job-triggered');

    // Wait for job completion
    const newSession = page.locator('.session-card').first();
    const terminalStatus = newSession.locator('.session-status--completed, .session-status--failed, .session-status--cancelled');
    await expect(terminalStatus).toBeVisible({ timeout: 90000 });

    // Verify it completed successfully
    await expect(newSession.locator('.session-status--completed')).toBeVisible();

    // Expand session to see I/O
    await newSession.click();

    // BUG FIX #1: Explicitly verify output block is visible (not just text somewhere)
    const outputBlock = newSession.locator('.io-block--output').first();
    await expect(outputBlock).toBeVisible({ timeout: 5000 });

    // Verify output block contains expected text
    await expect(outputBlock).toContainText('Codex smoke test OK', { timeout: 5000 });
    await screenshot(page, '14e-codex-job-completed');

    // BUG FIX #2: Check for log flickering - logs should remain visible if expanded
    const logsButton = newSession.locator('.io-logs-btn').first();
    if (await logsButton.isVisible()) {
      await logsButton.click();
      const inlineLogs = newSession.locator('.io-logs-inline').first();
      await expect(inlineLogs).toBeVisible({ timeout: 2000 });

      // Wait and verify logs don't collapse (flicker)
      await page.waitForTimeout(2000);
      await expect(inlineLogs).toBeVisible();
      await screenshot(page, '14f-codex-logs-stable');
    }
  });


  test('5b. Manual Codex Agent Selection', async ({ page }) => {
    // Login first
    await page.goto('/login');
    await page.fill('#password', PASSWORD);
    await page.click('button[type="submit"]');
    await expect(page).toHaveURL('/');

    // Click "New Task" button
    await page.click('button:has-text("New Task")');

    // Wait for modal
    await expect(page.locator('.modal-title:has-text("New Task")')).toBeVisible({ timeout: 5000 });
    await screenshot(page, '14g-manual-codex-task-modal');

    // Fill task form
    const promptInput = page.locator('textarea[placeholder="Describe the task..."]');
    await expect(promptInput).toBeVisible({ timeout: 5000 });
    await promptInput.fill('Reply with exactly: "Manual codex selection works"');

    // Select Manual context
    const contextSelect = page.locator('select').filter({ hasText: 'Manual' }).first();
    await contextSelect.selectOption('manual');

    // BUG FIX #3: Test manual selection of codex agent kind
    const agentKindSelect = page.locator('select#agent-kind-select');
    await expect(agentKindSelect).toBeVisible();
    await expect(agentKindSelect).toBeEnabled();

    // Select codex from dropdown
    await agentKindSelect.selectOption('codex');

    // Verify selection worked
    const selectedValue = await agentKindSelect.inputValue();
    expect(selectedValue).toBe('codex');
    await screenshot(page, '14h-codex-kind-selected');

    // Get initial session count
    const initialSessionCount = await page.locator('.session-card').count();

    // Submit the form
    await page.click('button:has-text("Submit Task")');

    // Wait for modal to close
    await expect(page.locator('.modal-backdrop--open')).toBeHidden({ timeout: 5000 });

    // Wait for new session to be created
    await expect(async () => {
      const newCount = await page.locator('.session-card').count();
      expect(newCount).toBeGreaterThan(initialSessionCount);
    }).toPass({ timeout: 10000, intervals: [1000] });

    // Get the new session
    const newSession = page.locator('.session-card').first();
    await expect(newSession).toBeVisible({ timeout: 10000 });

    // Wait for task completion
    const terminalStatus = newSession.locator('.session-status--completed, .session-status--failed, .session-status--cancelled');
    await expect(terminalStatus).toBeVisible({ timeout: 90000 });

    // Verify it completed successfully
    await expect(newSession.locator('.session-status--completed')).toBeVisible();

    // Expand session and verify output
    await newSession.click();

    // Verify output block is visible
    const outputBlock = newSession.locator('.io-block--output').first();
    await expect(outputBlock).toBeVisible({ timeout: 5000 });
    await expect(outputBlock).toContainText('Manual codex selection works', { timeout: 5000 });

    // Verify session used codex agent by checking Details tab
    const detailsTab = newSession.locator('.session-tab:has-text("Details")');
    await detailsTab.click();
    await expect(detailsTab).toHaveClass(/session-tab--active/);

    // Check that agent_url points to codex agent (port 19001 for smoke tests)
    await expect(newSession).toContainText('19001', { timeout: 5000 });
    await screenshot(page, '14i-manual-codex-verified');
  });

  test('6. UI Navigation and Interactions', async ({ page }) => {
    // Login first
    await page.goto('/login');
    await page.fill('#password', PASSWORD);
    await page.click('button[type="submit"]');
    await expect(page).toHaveURL('/');

    // Wait for dashboard to load with sessions
    const sessionCard = page.locator('.session-card').first();
    await expect(sessionCard).toBeVisible({ timeout: 10000 });
    await screenshot(page, '17-dashboard-with-sessions');

    // --- Fleet Section: Expand/Collapse ---
    const fleetTrigger = page.locator('.fleet-trigger');
    const fleetContent = page.locator('.fleet-content');

    // Initially fleet may be closed, ensure it's closed for test
    if (await fleetContent.isVisible()) {
      await fleetTrigger.click();
      await expect(fleetContent).toBeHidden();
    }
    await screenshot(page, '18-fleet-collapsed');

    // Expand fleet section
    await fleetTrigger.click();
    await expect(fleetContent).toBeVisible();
    await screenshot(page, '19-fleet-expanded');

    // Verify agent and helpers are shown
    await expect(page.locator('.fleet-category-label:has-text("Agents")')).toBeVisible();
    await expect(page.locator('.fleet-chip:has-text("idle"), .fleet-chip:has-text("working")').first()).toBeVisible();

    // Collapse fleet section
    await fleetTrigger.click();
    await expect(fleetContent).toBeHidden();
    await screenshot(page, '20-fleet-collapsed-again');

    // --- Session Card: Expand/Collapse ---
    // Session should be collapsed initially (from prior tests it might be expanded)
    const sessionBody = sessionCard.locator('.session-body');
    if (await sessionBody.isVisible()) {
      await sessionCard.locator('.session-header').click();
      await expect(sessionBody).toBeHidden();
    }
    await screenshot(page, '21-session-collapsed');

    // Expand session
    await sessionCard.locator('.session-header').click();
    await expect(sessionBody).toBeVisible();
    await screenshot(page, '22-session-expanded');

    // --- Session Tabs: Switch between I/O, Details, and Metrics ---
    const ioTab = sessionCard.locator('.session-tab:has-text("I/O")');
    const detailsTab = sessionCard.locator('.session-tab:has-text("Details")');
    const metricsTab = sessionCard.locator('.session-tab:has-text("Metrics")');

    // Verify I/O tab is active by default
    await expect(ioTab).toHaveClass(/session-tab--active/);
    await screenshot(page, '23-io-tab-active');

    // Switch to Details tab
    await detailsTab.click();
    await expect(detailsTab).toHaveClass(/session-tab--active/);
    await expect(ioTab).not.toHaveClass(/session-tab--active/);
    await screenshot(page, '24-details-tab-active');

    // Switch to Metrics tab
    await metricsTab.click();
    await expect(metricsTab).toHaveClass(/session-tab--active/);
    await expect(detailsTab).not.toHaveClass(/session-tab--active/);
    await screenshot(page, '25-metrics-tab-active');

    // Switch back to I/O tab
    await ioTab.click();
    await expect(ioTab).toHaveClass(/session-tab--active/);
    await screenshot(page, '26-back-to-io-tab');

    // Collapse session
    await sessionCard.locator('.session-header').click();
    await expect(sessionBody).toBeHidden();
    await screenshot(page, '27-session-collapsed-final');

    // --- Settings Modal: Open/Close ---
    // Settings button is in bottom nav bar (only visible on mobile viewport)
    // Temporarily switch to mobile viewport to access settings
    const originalViewport = page.viewportSize();
    await page.setViewportSize({ width: 375, height: 667 }); // iPhone SE size
    await screenshot(page, '28-mobile-viewport');

    const settingsButton = page.locator('.nav-item:has-text("Settings")');
    await expect(settingsButton).toBeVisible({ timeout: 5000 });
    await settingsButton.click();

    // Wait for settings modal
    const settingsModal = page.locator('.modal-backdrop--open');
    await expect(settingsModal).toBeVisible({ timeout: 5000 });
    await expect(page.locator('.modal-title:has-text("Settings")')).toBeVisible();
    await screenshot(page, '29-settings-modal-open');

    // Close settings via backdrop click
    await settingsModal.click({ position: { x: 10, y: 10 } }); // Click near edge (backdrop)
    await expect(settingsModal).toBeHidden({ timeout: 5000 });
    await screenshot(page, '30-settings-modal-closed');

    // Restore desktop viewport
    if (originalViewport) {
      await page.setViewportSize(originalViewport);
    }
    await screenshot(page, '31-back-to-desktop');

    // --- Task Modal: Open/Close with Escape ---
    await page.click('button:has-text("New Task")');
    await expect(page.locator('.modal-title:has-text("New Task")')).toBeVisible({ timeout: 5000 });
    await screenshot(page, '32-task-modal-open');

    // Close via Escape key
    await page.keyboard.press('Escape');
    await expect(page.locator('.modal-backdrop--open')).toBeHidden({ timeout: 5000 });
    await screenshot(page, '33-task-modal-closed-via-escape');

    // --- Keyboard Shortcuts ---
    // Click on main content to ensure no form element is focused
    await page.click('.main');

    // 'n' should open new task modal
    await page.keyboard.press('n');
    await expect(page.locator('.modal-title:has-text("New Task")')).toBeVisible({ timeout: 5000 });
    await screenshot(page, '34-task-modal-via-n-key');

    // Close it
    await page.keyboard.press('Escape');
    await expect(page.locator('.modal-backdrop--open')).toBeHidden({ timeout: 5000 });

    // Click again to unfocus any element after modal closes
    await page.click('.main');

    // 'f' should toggle fleet section (fleet is currently hidden from earlier test)
    await page.keyboard.press('f');
    await expect(fleetContent).toBeVisible({ timeout: 5000 });
    await screenshot(page, '35-fleet-toggled-via-f-key');

    await page.keyboard.press('f');
    await expect(fleetContent).toBeHidden({ timeout: 5000 });
    await screenshot(page, '36-fleet-toggled-again');

    // 'r' should refresh (we can verify by checking the data reloads)
    await page.keyboard.press('r');
    // Just verify page is still functional
    await expect(sessionCard).toBeVisible();
    await screenshot(page, '37-after-refresh');
  });
});
