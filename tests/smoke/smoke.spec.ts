import { test, expect } from '@playwright/test';

const PASSWORD = process.env.AG_WEB_PASSWORD || 'smoketest';

test.describe.serial('Agency Smoke Tests', () => {
  test('1. Login', async ({ page }) => {
    // Navigate to root - should redirect to login
    await page.goto('/');
    await expect(page).toHaveURL(/\/login/);

    // Fill password and submit
    await page.fill('#password', PASSWORD);
    await page.click('button[type="submit"]');

    // Verify dashboard loads
    await expect(page).toHaveURL('/');
    await expect(page.locator('.topbar')).toBeVisible();
  });

  test('2. Create Task', async ({ page }) => {
    // Login first
    await page.goto('/login');
    await page.fill('#password', PASSWORD);
    await page.click('button[type="submit"]');
    await expect(page).toHaveURL('/');

    // Click "New Task" button
    await page.click('button:has-text("New Task")');

    // Fill task form
    await page.fill('#prompt-input', 'What is 2+2? Reply with just the number.');

    // Select Manual context to enable model selection
    await page.selectOption('#context-select', 'manual');

    // Expand Advanced Options to access model select
    await page.click('button:has-text("Advanced Options")');
    await page.selectOption('#model-select', 'haiku');

    // Submit the form
    await page.click('button[type="submit"]:has-text("Submit")');

    // Wait for modal to close and task to appear
    await expect(page.locator('.modal-backdrop--open')).toBeHidden({ timeout: 5000 });

    // Wait for task completion (poll every 2s, max 90s)
    const sessionCard = page.locator('.session-card').first();
    await expect(sessionCard).toBeVisible({ timeout: 10000 });

    // Wait for task to reach a terminal state (completed, failed, or cancelled)
    const terminalStatus = sessionCard.locator('.session-status--completed, .session-status--failed, .session-status--cancelled');
    await expect(terminalStatus).toBeVisible({ timeout: 90000 });

    // Verify it completed successfully (not failed/cancelled)
    await expect(sessionCard.locator('.session-status--completed')).toBeVisible();

    // Expand card and verify output contains "4"
    await sessionCard.click();
    await expect(sessionCard).toContainText('4', { timeout: 5000 });
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

    // Click "+ New task in session"
    await page.click('button:has-text("New task in session")');

    // Fill the new task
    await page.fill('#prompt-input', 'What is 3+3? Reply with just the number.');

    // Submit
    await page.click('button[type="submit"]:has-text("Submit")');

    // Wait for modal to close
    await expect(page.locator('.modal-backdrop--open')).toBeHidden({ timeout: 5000 });

    // Wait for task to reach a terminal state
    const terminalStatus = sessionCard.locator('.session-status--completed, .session-status--failed, .session-status--cancelled');
    await expect(terminalStatus).toBeVisible({ timeout: 90000 });

    // Verify it completed successfully
    await expect(sessionCard.locator('.session-status--completed')).toBeVisible();
    await expect(sessionCard).toContainText('6', { timeout: 5000 });
  });

  test('4. Trigger Scheduled Job', async ({ page }) => {
    // Login first
    await page.goto('/login');
    await page.fill('#password', PASSWORD);
    await page.click('button[type="submit"]');
    await expect(page).toHaveURL('/');

    // Expand "Fleet" section
    await page.click('.fleet-trigger');
    await expect(page.locator('.fleet-content')).toBeVisible();

    // Wait for agent to show as idle (ensures previous task fully completed)
    // Poll multiple times to ensure agent is stable idle (not transitioning)
    await expect(async () => {
      const idleChip = page.locator('.fleet-chip:has-text("idle")');
      await expect(idleChip).toBeVisible();
      // Small delay to let discovery cache stabilize
      await page.waitForTimeout(500);
      await expect(idleChip).toBeVisible();
    }).toPass({ timeout: 10000, intervals: [1000] });

    // Find the smoke-test job and click "Run Now"
    const smokeTestJob = page.locator('.job-item').filter({
      hasText: 'smoke-test'
    });
    await expect(smokeTestJob).toBeVisible({ timeout: 5000 });

    // Get initial session count
    const initialSessionCount = await page.locator('.session-card').count();

    // Click trigger button
    await smokeTestJob.locator('button:has-text("Run Now")').click();

    // Verify new session created
    await expect(async () => {
      const newCount = await page.locator('.session-card').count();
      expect(newCount).toBeGreaterThan(initialSessionCount);
    }).toPass({ timeout: 10000, intervals: [1000] });

    // Wait for job completion
    const newSession = page.locator('.session-card').first();
    const terminalStatus = newSession.locator('.session-status--completed, .session-status--failed, .session-status--cancelled');
    await expect(terminalStatus).toBeVisible({ timeout: 90000 });

    // Verify it completed successfully
    await expect(newSession.locator('.session-status--completed')).toBeVisible();

    // Verify success state - output should contain "Smoke test OK"
    await newSession.click();
    await expect(newSession).toContainText('Smoke test OK', { timeout: 5000 });
  });
});
