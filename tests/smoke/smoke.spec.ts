import { test, expect, Page } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

const PASSWORD = process.env.AG_WEB_PASSWORD || 'smoketest';
const SCREENSHOT_DIR = path.join(__dirname, 'screenshots');

// Ensure screenshot directory exists
if (!fs.existsSync(SCREENSHOT_DIR)) {
  fs.mkdirSync(SCREENSHOT_DIR, { recursive: true });
}

async function screenshot(page: Page, name: string): Promise<void> {
  const filepath = path.join(SCREENSHOT_DIR, `${name}.png`);
  await page.screenshot({ path: filepath, fullPage: true });
  console.log(`Screenshot saved: ${filepath}`);
}

test.describe.serial('Agency Smoke Tests', () => {
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
    await promptInput.fill('What is 2+2? Reply with just the number.');

    // Select Manual context to enable model selection
    const contextSelect = page.locator('select').filter({ hasText: 'Manual' }).first();
    await contextSelect.selectOption('manual');

    // Expand Advanced Options to access model select
    await page.click('button:has-text("Advanced Options")');
    const modelSelect = page.getByLabel('Model');
    await modelSelect.selectOption('haiku');
    await screenshot(page, '04-task-form-filled');

    // Submit the form
    await page.click('button:has-text("Submit Task")');

    // Wait for modal to close and task to appear
    await expect(page.locator('.modal-backdrop--open')).toBeHidden({ timeout: 5000 });

    // Wait for task completion (poll every 2s, max 90s)
    const sessionCard = page.locator('.session-card').first();
    await expect(sessionCard).toBeVisible({ timeout: 10000 });
    await screenshot(page, '05-task-submitted');

    // Wait for task to reach a terminal state (completed, failed, or cancelled)
    const terminalStatus = sessionCard.locator('.session-status--completed, .session-status--failed, .session-status--cancelled');
    await expect(terminalStatus).toBeVisible({ timeout: 90000 });

    // Verify it completed successfully (not failed/cancelled)
    await expect(sessionCard.locator('.session-status--completed')).toBeVisible();

    // Expand card and verify output contains "4"
    await sessionCard.click();
    await expect(sessionCard).toContainText('4', { timeout: 5000 });
    await screenshot(page, '06-task-completed');
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
    await promptInput.fill('What is 3+3? Reply with just the number.');
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

    // Wait for I/O content to fully load (history loading to complete)
    // Use text locator for exact match to avoid multiple element issue
    await expect(sessionCard.getByText('Loading history...', { exact: true })).toBeHidden({ timeout: 10000 });

    // Now check for the expected output
    await expect(sessionCard).toContainText('6', { timeout: 5000 });
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
      const idleChip = page.locator('.fleet-chip:has-text("idle")');
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
    });
    await expect(smokeTestJob).toBeVisible({ timeout: 5000 });
  });

  test('5. Trigger Smoke Nightly Maintenance Job', async ({ page }) => {
    // Login first
    await page.goto('/login');
    await page.fill('#password', PASSWORD);
    await page.click('button[type="submit"]');
    await expect(page).toHaveURL('/');

    // Expand "Fleet" section
    await page.click('.fleet-trigger');
    await expect(page.locator('.fleet-content')).toBeVisible();

    // Wait for agent to show as idle
    await expect(async () => {
      const idleChip = page.locator('.fleet-chip:has-text("idle")');
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

    // Wait for job completion (longer timeout since this is a more complex job)
    const newSession = page.locator('.session-card').first();
    const terminalStatus = newSession.locator('.session-status--completed, .session-status--failed, .session-status--cancelled');
    await expect(terminalStatus).toBeVisible({ timeout: 180000 });

    // Verify it completed successfully
    await expect(newSession.locator('.session-status--completed')).toBeVisible();

    // Verify output contains expected content related to helloworld2
    await newSession.click();
    // The job should mention helloworld2 in its output since that's the target repo
    await expect(newSession).toContainText('helloworld2', { timeout: 5000 });
    await screenshot(page, '15-nightly-maintenance-completed');
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
    await screenshot(page, '16-dashboard-with-sessions');

    // --- Fleet Section: Expand/Collapse ---
    const fleetTrigger = page.locator('.fleet-trigger');
    const fleetContent = page.locator('.fleet-content');

    // Initially fleet may be closed, ensure it's closed for test
    if (await fleetContent.isVisible()) {
      await fleetTrigger.click();
      await expect(fleetContent).toBeHidden();
    }
    await screenshot(page, '17-fleet-collapsed');

    // Expand fleet section
    await fleetTrigger.click();
    await expect(fleetContent).toBeVisible();
    await screenshot(page, '18-fleet-expanded');

    // Verify agent and helpers are shown
    await expect(page.locator('.fleet-category-label:has-text("Agents")')).toBeVisible();
    await expect(page.locator('.fleet-chip:has-text("idle"), .fleet-chip:has-text("working")')).toBeVisible();

    // Collapse fleet section
    await fleetTrigger.click();
    await expect(fleetContent).toBeHidden();
    await screenshot(page, '19-fleet-collapsed-again');

    // --- Session Card: Expand/Collapse ---
    // Session should be collapsed initially (from prior tests it might be expanded)
    const sessionBody = sessionCard.locator('.session-body');
    if (await sessionBody.isVisible()) {
      await sessionCard.locator('.session-header').click();
      await expect(sessionBody).toBeHidden();
    }
    await screenshot(page, '20-session-collapsed');

    // Expand session
    await sessionCard.locator('.session-header').click();
    await expect(sessionBody).toBeVisible();
    await screenshot(page, '21-session-expanded');

    // --- Session Tabs: Switch between I/O, Details, and Metrics ---
    const ioTab = sessionCard.locator('.session-tab:has-text("I/O")');
    const detailsTab = sessionCard.locator('.session-tab:has-text("Details")');
    const metricsTab = sessionCard.locator('.session-tab:has-text("Metrics")');

    // Verify I/O tab is active by default
    await expect(ioTab).toHaveClass(/session-tab--active/);
    await screenshot(page, '22-io-tab-active');

    // Switch to Details tab
    await detailsTab.click();
    await expect(detailsTab).toHaveClass(/session-tab--active/);
    await expect(ioTab).not.toHaveClass(/session-tab--active/);
    await screenshot(page, '23-details-tab-active');

    // Switch to Metrics tab
    await metricsTab.click();
    await expect(metricsTab).toHaveClass(/session-tab--active/);
    await expect(detailsTab).not.toHaveClass(/session-tab--active/);
    await screenshot(page, '24-metrics-tab-active');

    // Switch back to I/O tab
    await ioTab.click();
    await expect(ioTab).toHaveClass(/session-tab--active/);
    await screenshot(page, '25-back-to-io-tab');

    // Collapse session
    await sessionCard.locator('.session-header').click();
    await expect(sessionBody).toBeHidden();
    await screenshot(page, '26-session-collapsed-final');

    // --- Settings Modal: Open/Close ---
    // Settings button is in bottom nav bar (only visible on mobile viewport)
    // Temporarily switch to mobile viewport to access settings
    const originalViewport = page.viewportSize();
    await page.setViewportSize({ width: 375, height: 667 }); // iPhone SE size
    await screenshot(page, '27-mobile-viewport');

    const settingsButton = page.locator('.nav-item:has-text("Settings")');
    await expect(settingsButton).toBeVisible({ timeout: 5000 });
    await settingsButton.click();

    // Wait for settings modal
    const settingsModal = page.locator('.modal-backdrop--open');
    await expect(settingsModal).toBeVisible({ timeout: 5000 });
    await expect(page.locator('.modal-title:has-text("Settings")')).toBeVisible();
    await screenshot(page, '28-settings-modal-open');

    // Close settings via backdrop click
    await settingsModal.click({ position: { x: 10, y: 10 } }); // Click near edge (backdrop)
    await expect(settingsModal).toBeHidden({ timeout: 5000 });
    await screenshot(page, '29-settings-modal-closed');

    // Restore desktop viewport
    if (originalViewport) {
      await page.setViewportSize(originalViewport);
    }
    await screenshot(page, '30-back-to-desktop');

    // --- Task Modal: Open/Close with Escape ---
    await page.click('button:has-text("New Task")');
    await expect(page.locator('.modal-title:has-text("New Task")')).toBeVisible({ timeout: 5000 });
    await screenshot(page, '31-task-modal-open');

    // Close via Escape key
    await page.keyboard.press('Escape');
    await expect(page.locator('.modal-backdrop--open')).toBeHidden({ timeout: 5000 });
    await screenshot(page, '32-task-modal-closed-via-escape');

    // --- Keyboard Shortcuts ---
    // Click on main content to ensure no form element is focused
    await page.click('.main');

    // 'n' should open new task modal
    await page.keyboard.press('n');
    await expect(page.locator('.modal-title:has-text("New Task")')).toBeVisible({ timeout: 5000 });
    await screenshot(page, '33-task-modal-via-n-key');

    // Close it
    await page.keyboard.press('Escape');
    await expect(page.locator('.modal-backdrop--open')).toBeHidden({ timeout: 5000 });

    // Click again to unfocus any element after modal closes
    await page.click('.main');

    // 'f' should toggle fleet section (fleet is currently hidden from earlier test)
    await page.keyboard.press('f');
    await expect(fleetContent).toBeVisible({ timeout: 5000 });
    await screenshot(page, '34-fleet-toggled-via-f-key');

    await page.keyboard.press('f');
    await expect(fleetContent).toBeHidden({ timeout: 5000 });
    await screenshot(page, '35-fleet-toggled-again');

    // 'r' should refresh (we can verify by checking the data reloads)
    await page.keyboard.press('r');
    // Just verify page is still functional
    await expect(sessionCard).toBeVisible();
    await screenshot(page, '36-after-refresh');
  });
});
