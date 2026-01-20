# Web UI Design

This document specifies the design for Agency's new observability web UI layer.

**Related:** [PLAN.md](PLAN.md) Phase 2.1 Observability

---

## Design Philosophy

### Danish Minimalism Principles

The UI follows Danish minimalist design principles:

1. **Functional simplicity** - Every element serves a purpose; no decorative clutter
2. **Quiet confidence** - Subtle, refined aesthetics over bold or flashy design
3. **Generous whitespace** - Content breathes; density is controlled
4. **Typography-first** - Clean sans-serif fonts carry the visual hierarchy
5. **Muted palette** - Low-contrast, sophisticated colors with purposeful accents
6. **Quality materials** - Premium feel through subtle shadows, borders, and transitions

### Dark Mode Implementation

Dark mode is the default and only mode, optimized for:
- Extended viewing in low-light environments
- Reduced eye strain during long monitoring sessions
- Visual distinction of status indicators
- Code and log output readability

---

## Visual Design System

### Color Palette

```
Background Layers:
  --bg-base:        #0d0d0f      /* Deepest background */
  --bg-surface:     #151518      /* Cards, panels */
  --bg-elevated:    #1c1c21      /* Modals, dropdowns */
  --bg-hover:       #232329      /* Interactive hover states */

Text:
  --text-primary:   #e8e8eb      /* Primary content */
  --text-secondary: #9898a0      /* Secondary, muted */
  --text-tertiary:  #5c5c64      /* Disabled, placeholders */

Borders:
  --border-subtle:  #28282f      /* Subtle separators */
  --border-default: #3a3a42      /* Default borders */

Status Colors:
  --status-idle:    #4ade80      /* Green - idle/success */
  --status-working: #60a5fa      /* Blue - in progress */
  --status-error:   #f87171      /* Red - failed/error */
  --status-warning: #fbbf24      /* Amber - warning */
  --status-pending: #a78bfa      /* Purple - pending/queued */

Accents:
  --accent-primary: #3b82f6      /* Interactive elements */
  --accent-hover:   #60a5fa      /* Hover state */
```

### Typography

```
Font Stack:
  --font-sans: 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif
  --font-mono: 'JetBrains Mono', 'SF Mono', 'Fira Code', monospace

Scale:
  --text-xs:   0.75rem   (12px)  /* Timestamps, metadata */
  --text-sm:   0.875rem  (14px)  /* Secondary content */
  --text-base: 1rem      (16px)  /* Body text */
  --text-lg:   1.125rem  (18px)  /* Subheadings */
  --text-xl:   1.25rem   (20px)  /* Section titles */
  --text-2xl:  1.5rem    (24px)  /* Page titles */

Weights:
  --font-normal: 400
  --font-medium: 500
  --font-semibold: 600
```

### Spacing System

```
--space-1:  0.25rem  (4px)
--space-2:  0.5rem   (8px)
--space-3:  0.75rem  (12px)
--space-4:  1rem     (16px)
--space-5:  1.25rem  (20px)
--space-6:  1.5rem   (24px)
--space-8:  2rem     (32px)
--space-10: 2.5rem   (40px)
--space-12: 3rem     (48px)
```

### Border Radius

```
--radius-sm: 4px     /* Buttons, inputs */
--radius-md: 6px     /* Cards */
--radius-lg: 8px     /* Modals, panels */
--radius-xl: 12px    /* Large containers */
```

---

## Mobile-First Responsive Design

### iPhone 17 Pro Optimization

Primary breakpoint targets:

| Device | Width | Design Focus |
|--------|-------|--------------|
| iPhone 17 Pro | 402px | Primary mobile target |
| iPhone 17 Pro Max | 440px | Large mobile |
| iPad Mini | 768px | Tablet portrait |
| iPad Pro 11" | 834px | Tablet landscape |
| Desktop | 1024px+ | Full layout |

### Mobile-Specific Considerations

1. **Touch targets** - Minimum 44x44px for all interactive elements
2. **Bottom navigation** - Primary actions accessible with thumb
3. **Swipe gestures** - Expand/collapse panels, dismiss modals
4. **Safe areas** - Respect iOS notch and home indicator
5. **Pull-to-refresh** - Native-feeling data refresh
6. **Haptic feedback** - System haptics on key interactions

### Responsive Layout Strategy

```
Mobile (< 768px):
  - Single column layout
  - Stacked cards
  - Bottom sheet for details
  - Floating action button for task submission
  - Collapsible sidebar as overlay

Tablet (768px - 1023px):
  - Two column layout
  - List + detail split view
  - Side panel for navigation

Desktop (1024px+):
  - Three column layout
  - Persistent sidebar navigation
  - Inline detail panels
  - Keyboard shortcuts enabled
```

---

## Information Architecture

### Hierarchy

```
Dashboard (root)
├── Fleet Overview
│   ├── Agents (list with status)
│   ├── Directors (list with status)
│   └── Helpers (list with status)
│
├── Sessions (primary view)
│   ├── Session Card
│   │   ├── Summary (30 char from first task)
│   │   ├── Status indicator
│   │   ├── Agent badge
│   │   ├── Timestamp
│   │   └── Expand/collapse control
│   │
│   └── Session Detail (expanded)
│       ├── Session metadata
│       ├── Tasks (list)
│       │   ├── Task summary
│       │   ├── Status
│       │   ├── Duration
│       │   └── Expand control
│       │
│       └── Task Detail (expanded)
│           ├── Full prompt
│           ├── Output (scrollable)
│           ├── Steps/traces (if available)
│           └── Metrics (tokens, cost, time)
│
├── Task Submission
│   ├── Agent selector
│   ├── Prompt input
│   └── Options (tier, timeout)
│
└── Settings
    ├── Paired devices
    └── Preferences
```

### Navigation Pattern

- **Mobile:** Bottom tab bar with 4 primary sections
- **Desktop:** Left sidebar with persistent navigation
- **Universal:** Breadcrumb trail for nested views

---

## Component Specifications

### 1. Session Card (Compact)

The primary UI element for displaying sessions in a list.

```
┌─────────────────────────────────────────────────────────┐
│  ● Fix auth token refresh      │  agent-01   2m ago   ▼│
└─────────────────────────────────────────────────────────┘
   │                                   │          │      │
   │                                   │          │      └─ Expand
   │                                   │          └─ Relative time
   │                                   └─ Agent badge (truncated)
   └─ Task summary (30 char max) with status dot
```

**States:**
- Default: Collapsed, shows summary only
- Expanded: Shows full session detail inline
- Active: Subtle pulse animation on status dot

### 2. Session Detail (Expanded)

When a session card is expanded:

```
┌─────────────────────────────────────────────────────────┐
│  ● Fix auth token refresh      │  agent-01   2m ago   ▲│
├─────────────────────────────────────────────────────────┤
│                                                         │
│  Session: sess_a1b2c3                                   │
│  Source: web  │  Started: 14:23:05                      │
│                                                         │
│  Tasks                                                  │
│  ┌─────────────────────────────────────────────────┐   │
│  │ ✓ Fix auth token refresh           45s      ▼  │   │
│  └─────────────────────────────────────────────────┘   │
│  ┌─────────────────────────────────────────────────┐   │
│  │ ● Add retry logic for API          working  ▼  │   │
│  └─────────────────────────────────────────────────┘   │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

### 3. Task Detail (Expanded)

When a task within a session is expanded:

```
┌─────────────────────────────────────────────────────────┐
│ ✓ Fix auth token refresh                   45s      ▲  │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  Prompt                                                 │
│  ┌─────────────────────────────────────────────────┐   │
│  │ Fix the authentication token refresh logic in   │   │
│  │ the API client. The tokens expire but the...    │   │
│  └─────────────────────────────────────────────────┘   │
│                                                         │
│  Output                                          ⤢     │
│  ┌─────────────────────────────────────────────────┐   │
│  │ I'll analyze the auth client code and fix the   │   │
│  │ token refresh logic.                            │   │
│  │                                                 │   │
│  │ **Changes Made:**                               │   │
│  │ 1. Added refresh threshold check                │   │
│  │ 2. Implemented automatic token renewal...       │   │
│  │                                                 │   │
│  │                                    [Show more ▼]│   │
│  └─────────────────────────────────────────────────┘   │
│                                                         │
│  Metrics                                                │
│  ┌──────────┬──────────┬──────────┬──────────┐         │
│  │ Tokens   │ Cost     │ Duration │ Model    │         │
│  │ 12.4k    │ $0.037   │ 45s      │ sonnet   │         │
│  └──────────┴──────────┴──────────┴──────────┘         │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

### 4. Fleet Overview Panel

Collapsible by default on mobile, persistent on desktop.

```
┌─────────────────────────────────────────────────────────┐
│  Fleet                                              ▼   │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  Agents                                                 │
│  ┌─────────────────────────────────────────────────┐   │
│  │ ● agent-01    idle       localhost:9001         │   │
│  │ ● agent-02    working    localhost:9002         │   │
│  └─────────────────────────────────────────────────┘   │
│                                                         │
│  Directors                                              │
│  ┌─────────────────────────────────────────────────┐   │
│  │ ○ scheduler   2 jobs     localhost:9100         │   │
│  └─────────────────────────────────────────────────┘   │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

### 5. Task Submission Modal

Full-screen on mobile, modal on desktop.

```
┌─────────────────────────────────────────────────────────┐
│  New Task                                          ✕    │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  Agent                                                  │
│  ┌─────────────────────────────────────────────────┐   │
│  │ agent-01 (idle)                              ▼  │   │
│  └─────────────────────────────────────────────────┘   │
│                                                         │
│  Prompt                                                 │
│  ┌─────────────────────────────────────────────────┐   │
│  │                                                 │   │
│  │                                                 │   │
│  │                                                 │   │
│  └─────────────────────────────────────────────────┘   │
│                                                         │
│  Options (tier, timeout)                           ▼    │
│                                                         │
│  ┌─────────────────────────────────────────────────┐   │
│  │                                      Submit ▶   │   │
│  └─────────────────────────────────────────────────┘   │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

---

## API Surface

See [REFERENCE.md](REFERENCE.md) for the current dashboard, task, and queue endpoints.

---

## Architectural Decisions

### Real-Time Updates: Polling

**Decision:** Use HTTP polling for real-time updates (not WebSocket/SSE).

**Rationale:**
- Simpler implementation and debugging
- Works reliably across all network conditions
- No connection state management complexity
- Sufficient for dashboard use case (sub-second updates not required)
- Easier to implement ETag-based caching for efficiency

### Session/Task Hierarchy: Flat

**Decision:** Use a flat session list (not nested/hierarchical traces).

**Rationale:**
- Simpler mental model for users
- Sessions are the primary unit of work
- Each session contains one task (or a linear sequence)
- Avoids complexity of tree navigation
- Better mobile experience with simpler layouts

### Offline Support: None

**Decision:** No offline caching or localStorage persistence.

**Rationale:**
- Dashboard is for real-time monitoring
- Stale cached data could be misleading
- Reduces implementation complexity
- Users expect fresh data when viewing dashboard
- No PWA requirements

---

## State Management

### Client-Side Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    UI Components                        │
│  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐    │
│  │ Fleet   │  │Sessions │  │ Task    │  │Settings │    │
│  │ Panel   │  │  List   │  │ Submit  │  │  Modal  │    │
│  └────┬────┘  └────┬────┘  └────┬────┘  └────┬────┘    │
│       │            │            │            │          │
│       └────────────┴────────────┴────────────┘          │
│                         │                               │
│                    ┌────┴────┐                          │
│                    │  Store  │                          │
│                    └────┬────┘                          │
│                         │                               │
│    ┌────────────────────┼────────────────────┐          │
│    │                    │                    │          │
│ ┌──┴──┐  ┌──────┐  ┌───┴────┐  ┌─────────┐ │          │
│ │Fleet│  │Sessions│  │Active  │  │Settings │ │          │
│ │State│  │ State  │  │Task    │  │ State   │ │          │
│ └─────┘  └────────┘  └────────┘  └─────────┘ │          │
│                                               │          │
└───────────────────────────────────────────────┼──────────┘
                                                │
                                           ┌────┴────┐
                                           │  API    │
                                           │ Client  │
                                           └────┬────┘
                                                │
                                        ┌───────┴───────┐
                                        │   REST API    │
                                        └───────────────┘
```

### State Shape

```typescript
interface AppState {
  fleet: {
    agents: ComponentStatus[];
    directors: ComponentStatus[];
    helpers: ComponentStatus[];
    lastUpdated: number;
  };

  sessions: {
    items: Session[];       // Flat list of sessions
    expanded: string | null; // Single expanded session (accordion)
    lastUpdated: number;
  };

  activeTask: {
    taskId: string | null;
    sessionId: string | null;
    agentUrl: string | null;
    pollInterval: number;
  };

  ui: {
    fleetPanelOpen: boolean;
    taskModalOpen: boolean;
    settingsModalOpen: boolean;
  };
}
```

### Polling Strategy

**Implementation:** HTTP polling with visibility-based pausing.

```
Idle State:
  - Fleet status: every 5s
  - Sessions: every 5s (with ETag for efficiency)

Active Task State:
  - Active task: every 1s
  - Fleet status: every 2s
  - Sessions: every 2s

Background Tab:
  - All polling paused (visibility API)
  - Resume immediately on tab focus
  - Visual indicator shows polling status
```

**Polling Implementation:**
```javascript
// Pause when tab hidden, resume when visible
document.addEventListener('visibilitychange', () => {
  if (document.hidden) {
    stopPolling();
  } else {
    startPolling();
    refresh(); // Immediate fetch on resume
  }
});
```

---

## Accessibility

### WCAG 2.1 AA Compliance

1. **Color contrast** - Minimum 4.5:1 for text, 3:1 for UI elements
2. **Focus indicators** - Visible focus rings on all interactive elements
3. **Keyboard navigation** - Full keyboard accessibility
4. **Screen reader support** - Semantic HTML, ARIA labels
5. **Motion reduction** - Respect `prefers-reduced-motion`

### Keyboard Shortcuts (Desktop)

| Key | Action |
|-----|--------|
| `n` | New task |
| `r` | Refresh dashboard |
| `f` | Toggle fleet panel |
| `j/k` | Navigate sessions up/down |
| `Enter` | Expand/collapse selected |
| `Escape` | Close modals, deselect |

---

## Implementation Approach

### Technology Stack

**Selected:** Alpine.js

**Rationale:**
- Declarative reactivity without build step
- 15KB minified, loads from CDN
- Simple directives (`x-data`, `x-show`, `x-for`, `x-on`)
- Alpine stores for shared state
- Easy to understand and modify
- Better DX than vanilla JS for reactive UI
- Good fit for dashboard complexity level

**Not selected:**
- *Vanilla JS* - More boilerplate for reactive state
- *Preact/React* - Overkill, requires build step
- *HTMX* - Better for server-rendered apps, not SPAs

### File Structure

With Alpine.js, the entire UI lives in a single HTML file with inline styles and scripts:

```
internal/view/web/
├── templates/
│   ├── dashboard.html         # Single-file Alpine.js SPA (~1500 LOC)
│   │                          # Contains: CSS, Alpine stores, components, HTML
│   ├── login.html             # Existing auth page
│   └── pair.html              # Existing device pairing
├── handlers.go                # Simplified: just serve SPA + API endpoints
└── embed.go                   # Go embed directives
```

**Why single file:**
- No build step or bundling
- Easy to modify and deploy
- All dependencies from CDN (Alpine.js)
- Matches existing login.html/pair.html pattern
- Mockup already demonstrates this works well

### CSS Architecture

```css
/* Design tokens */
:root {
  /* Colors, spacing, typography from design system */
}

/* Base reset */
*, *::before, *::after {
  box-sizing: border-box;
}

/* Component styles using BEM naming */
.session-card { }
.session-card__header { }
.session-card__header--expanded { }
.session-card__body { }

/* Utility classes for common patterns */
.u-visually-hidden { }
.u-flex { }
.u-gap-4 { }
```

---

## Maintainability Guidelines

### Code Organization

1. **Single responsibility** - Each component does one thing
2. **Explicit dependencies** - No implicit globals
3. **Consistent naming** - BEM for CSS, camelCase for JS
4. **Documentation** - JSDoc comments on all public functions

### Update Workflow

To modify the UI:

1. **Design tokens** - Update CSS custom properties for global changes
2. **Component styles** - Modify component-specific CSS
3. **Component logic** - Update individual JS modules
4. **Templates** - Minimal changes needed (structure only)

### Testing Strategy

1. **Visual regression** - Screenshot tests for components
2. **Unit tests** - Pure functions (summary generation, formatting)
3. **Integration tests** - API client + state management
4. **E2E tests** - Critical user flows (submit task, view results)

---

## Mockups

- `mockup-final.html` - Current visual reference
- `mockup-basic.html`, `mockup-creative.html`, `mockup-langsmith.html` - Design explorations

---

## Implementation Status

- Implemented as a single-file Alpine.js SPA in `internal/view/web/templates/dashboard.html`.
- Full replacement of the old dashboard templates; login/pair pages remain server-rendered.
- Fleet summary counts agents only; full breakdown shown when expanded.
- Live output uses polling for active tasks.

---

## Open Questions

- Export/sharing of sessions (JSON, share links).
- Multi-agent views when a director orchestrates multiple agents.

---

## Edge Cases & Race Conditions

This section documents potential race conditions, edge cases, and synchronization issues that must be handled correctly.

### Polling Race Conditions

#### 1. Out-of-Order Response Arrival

**Problem:** When polling rapidly (1s intervals during active tasks), network latency can cause responses to arrive out of order. A request sent at T+1s might return after a request sent at T+2s.

**Solution:** Use request sequencing with AbortController:

```javascript
// Track current request to cancel stale ones
let currentController = null;
let requestSequence = 0;

async function fetchDashboard() {
    // Cancel any in-flight request
    if (currentController) {
        currentController.abort();
    }

    currentController = new AbortController();
    const mySequence = ++requestSequence;

    try {
        const response = await fetch('/api/dashboard', {
            signal: currentController.signal
        });

        // Verify this is still the latest request
        if (mySequence !== requestSequence) {
            return; // Stale response, discard
        }

        const data = await response.json();
        updateState(data);
    } catch (err) {
        if (err.name === 'AbortError') {
            // Request was cancelled, this is expected
            return;
        }
        handleError(err);
    }
}
```

#### 2. Tab Visibility Transitions

**Problem:** User rapidly switches tabs (hidden → visible → hidden). If not handled, multiple polling loops can start.

**Solution:** Guard against concurrent polling initialization:

```javascript
let isPolling = false;
let pollTimer = null;

function startPolling() {
    if (isPolling) return; // Already polling
    isPolling = true;

    // Immediate fetch on resume
    refresh();

    pollTimer = setInterval(() => {
        if (!document.hidden) {
            refresh();
        }
    }, this.activeTask ? 1000 : 5000);
}

function stopPolling() {
    isPolling = false;
    if (pollTimer) {
        clearInterval(pollTimer);
        pollTimer = null;
    }
    // Cancel any in-flight requests
    if (currentController) {
        currentController.abort();
        currentController = null;
    }
}

document.addEventListener('visibilitychange', () => {
    if (document.hidden) {
        stopPolling();
    } else {
        startPolling();
    }
});
```

#### 3. Stale "Working" Task Detection

**Problem:** If the dashboard loads while an agent was mid-task, but that task has since completed (agent restarted, network partition, etc.), the UI shows a perpetually "working" task.

**Solution:** Reconciliation on initial load and periodic verification:

```javascript
async function reconcileWorkingSessions() {
    const workingTasks = sessions
        .flatMap(s => s.tasks)
        .filter(t => t.state === 'working');

    for (const task of workingTasks) {
        try {
            // Query agent directly for current task state
            const response = await fetch(`${task.agent_url}/task/${task.task_id}`);
            if (response.status === 404) {
                // Task no longer exists on agent - mark as unknown
                task.state = 'unknown';
                task.stale_reason = 'Task not found on agent';
            } else {
                const current = await response.json();
                if (current.state !== 'working') {
                    // Update to actual state
                    task.state = current.state;
                    task.output = current.output;
                }
            }
        } catch (err) {
            // Agent unreachable - mark task state as uncertain
            task.state = 'unknown';
            task.stale_reason = 'Agent unreachable';
        }
    }
}
```

### Optimistic UI Edge Cases

#### 1. Task Submission Failure

**Problem:** User submits a task, UI optimistically shows it as "working", but the actual submission fails.

**Solution:** Implement proper rollback:

```javascript
async function submitTask(prompt, agentUrl) {
    // Optimistically add to UI
    const optimisticTask = {
        task_id: 'pending-' + Date.now(),
        state: 'submitting', // Special state for optimistic updates
        prompt: prompt,
        isOptimistic: true
    };

    addTaskToSession(optimisticTask);

    try {
        const response = await fetch('/api/task', {
            method: 'POST',
            body: JSON.stringify({ agent_url: agentUrl, prompt }),
            headers: { 'Content-Type': 'application/json' }
        });

        if (!response.ok) {
            throw new Error(await response.text());
        }

        const result = await response.json();

        // Replace optimistic task with real one
        replaceOptimisticTask(optimisticTask.task_id, {
            task_id: result.task_id,
            state: 'working',
            prompt: prompt,
            isOptimistic: false
        });

    } catch (err) {
        // Rollback: Remove optimistic task, show error
        removeOptimisticTask(optimisticTask.task_id);
        showError(`Task submission failed: ${err.message}`);
    }
}
```

#### 2. Agent State Change During Submission

**Problem:** Agent shows as "idle" when user opens task modal, but becomes "working" before submission completes.

**Solution:** Double-check agent state before submission:

```javascript
async function submitTask(prompt, agentUrl) {
    // Pre-flight check
    const agentStatus = await fetch(`${agentUrl}/status`);
    const status = await agentStatus.json();

    if (status.state !== 'idle') {
        showError(`Agent is now ${status.state}. Please select another agent.`);
        // Refresh agent list
        await refreshFleet();
        return;
    }

    // Proceed with submission...
}
```

### Session State Synchronization

#### 1. Session Created Externally

**Problem:** A session is created via CLI or another client while the dashboard is open. The dashboard should show it without requiring manual refresh.

**Solution:** Polling handles this automatically, but ensure proper merge logic:

```javascript
function mergeSessionUpdate(existingSessions, newSessions) {
    const merged = [...existingSessions];

    for (const newSession of newSessions) {
        const existing = merged.find(s => s.id === newSession.id);

        if (!existing) {
            // New session - add it
            merged.push(newSession);
        } else {
            // Existing session - update carefully
            // Preserve UI state (expanded, scroll position)
            existing.tasks = newSession.tasks;
            existing.updated_at = newSession.updated_at;
            // Don't overwrite: existing.isExpanded, existing.scrollTop
        }
    }

    // Sort by updated_at (newest first)
    return merged.sort((a, b) =>
        new Date(b.updated_at) - new Date(a.updated_at)
    );
}
```

#### 2. Concurrent Task in Same Session

**Problem:** User has a session expanded, viewing task output. A second task starts in that session (e.g., follow-up command). The new task should appear without disrupting the current view.

**Solution:** Append-only updates for tasks within expanded sessions:

```javascript
function updateSessionTasks(session, newTasks) {
    // Only add new tasks, never remove or reorder existing
    for (const task of newTasks) {
        if (!session.tasks.find(t => t.task_id === task.task_id)) {
            session.tasks.push(task);
        } else {
            // Update existing task in place
            const existing = session.tasks.find(t => t.task_id === task.task_id);
            Object.assign(existing, task);
        }
    }
}
```

---

## API Implementation Details

### ETag-Based Caching

The `/api/dashboard` endpoint supports conditional requests using ETags to minimize bandwidth on unchanged data.

#### Server Implementation (Go)

```go
func (h *Handlers) HandleDashboard(w http.ResponseWriter, r *http.Request) {
    data := h.buildDashboardData()

    // Generate ETag from data hash
    hash := sha256.Sum256([]byte(fmt.Sprintf("%v", data)))
    etag := fmt.Sprintf(`"%x"`, hash[:8])

    // Check If-None-Match header
    if match := r.Header.Get("If-None-Match"); match == etag {
        w.WriteHeader(http.StatusNotModified)
        return
    }

    w.Header().Set("ETag", etag)
    w.Header().Set("Cache-Control", "private, no-cache")
    json.NewEncoder(w).Encode(data)
}
```

#### Client Implementation

```javascript
let lastETag = null;

async function fetchDashboard() {
    const headers = {};
    if (lastETag) {
        headers['If-None-Match'] = lastETag;
    }

    const response = await fetch('/api/dashboard', { headers });

    if (response.status === 304) {
        // Data unchanged, skip update
        return;
    }

    lastETag = response.headers.get('ETag');
    const data = await response.json();
    updateState(data);
}
```

#### ETag Generation Strategies

| Strategy | Pros | Cons |
|----------|------|------|
| Content hash (SHA256) | Accurate, catches any change | CPU cost for large responses |
| Last-modified timestamp | Simple, efficient | May miss changes if clock skews |
| Version counter | Very fast | Requires additional state tracking |

**Recommendation:** Use content hash for `/api/dashboard` (small payload), timestamp for `/api/task/:id/output` (large, append-only).

### Request Timeout Handling

All API requests should have timeouts to prevent hanging UI:

```javascript
async function fetchWithTimeout(url, options = {}, timeoutMs = 10000) {
    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), timeoutMs);

    try {
        const response = await fetch(url, {
            ...options,
            signal: controller.signal
        });
        clearTimeout(timeoutId);
        return response;
    } catch (err) {
        clearTimeout(timeoutId);
        if (err.name === 'AbortError') {
            throw new Error(`Request timed out after ${timeoutMs}ms`);
        }
        throw err;
    }
}
```

---

## Task State Machine

Tasks follow a finite state machine with well-defined transitions:

```
                    ┌──────────────┐
                    │   pending    │
                    │  (queued)    │
                    └──────┬───────┘
                           │ agent accepts
                           ▼
                    ┌──────────────┐
        ┌──────────│   working    │──────────┐
        │          │ (executing)  │          │
        │          └──────┬───────┘          │
        │ cancel          │ completes        │ error
        ▼                 ▼                  ▼
┌──────────────┐  ┌──────────────┐  ┌──────────────┐
│  cancelled   │  │  completed   │  │   failed     │
│              │  │              │  │              │
└──────────────┘  └──────────────┘  └──────────────┘
        │                 │                  │
        └─────────────────┴──────────────────┘
                          │
                    ┌─────┴─────┐
                    │  Terminal │
                    │  States   │
                    └───────────┘
```

### State Definitions

| State | Description | UI Treatment |
|-------|-------------|--------------|
| `pending` | Task queued but not yet started | Gray dot, "Queued" label |
| `working` | Task actively executing | Blue dot with pulse animation, "Working" label |
| `completed` | Task finished successfully | Green checkmark |
| `failed` | Task encountered error | Red X, show error message |
| `cancelled` | Task was cancelled by user | Gray dash, "Cancelled" label |
| `unknown` | State uncertain (agent unreachable) | Yellow warning icon, "Unknown" label |

### State Transition Validation

```javascript
const VALID_TRANSITIONS = {
    'pending': ['working', 'cancelled'],
    'working': ['completed', 'failed', 'cancelled'],
    'completed': [],  // Terminal
    'failed': [],     // Terminal
    'cancelled': [],  // Terminal
    'unknown': ['working', 'completed', 'failed', 'cancelled'] // Can resolve to any
};

function isValidTransition(fromState, toState) {
    return VALID_TRANSITIONS[fromState]?.includes(toState) ?? false;
}

function updateTaskState(task, newState) {
    if (!isValidTransition(task.state, newState)) {
        console.warn(`Invalid state transition: ${task.state} → ${newState}`);
        // Still update for display, but log warning
    }
    task.state = newState;
}
```

---

## iOS-Specific Implementation

### Safe Area Insets

The dashboard must respect iOS safe areas to avoid content being hidden by the notch or home indicator.

#### HTML Viewport Configuration

```html
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
```

#### CSS Safe Area Implementation

```css
/* Root container */
.app-container {
    padding-top: env(safe-area-inset-top);
    padding-right: env(safe-area-inset-right);
    padding-bottom: env(safe-area-inset-bottom);
    padding-left: env(safe-area-inset-left);
}

/* Fixed header */
.header {
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    padding-top: calc(var(--space-4) + env(safe-area-inset-top));
    padding-left: env(safe-area-inset-left);
    padding-right: env(safe-area-inset-right);
}

/* Fixed bottom navigation/FAB */
.fab {
    position: fixed;
    bottom: calc(var(--space-6) + env(safe-area-inset-bottom));
    right: calc(var(--space-4) + env(safe-area-inset-right));
}

/* Full-screen modal on mobile */
.modal--fullscreen {
    padding-top: env(safe-area-inset-top);
    padding-bottom: env(safe-area-inset-bottom);
}
```

### Haptic Feedback

Safari on iOS does not support the standard `navigator.vibrate()` API. However, iOS 18+ provides haptic feedback through a checkbox switch workaround.

#### Implementation

```javascript
// Create hidden haptic trigger element
function createHapticTrigger() {
    const input = document.createElement('input');
    input.type = 'checkbox';
    input.setAttribute('switch', '');
    input.style.cssText = 'position:absolute;opacity:0;pointer-events:none;';
    input.id = 'haptic-trigger';

    const label = document.createElement('label');
    label.setAttribute('for', 'haptic-trigger');
    label.style.cssText = 'position:absolute;opacity:0;pointer-events:none;';

    document.body.appendChild(input);
    document.body.appendChild(label);

    return label;
}

let hapticLabel = null;

function triggerHaptic() {
    // Try standard API first (Android, desktop)
    if (navigator.vibrate) {
        navigator.vibrate(10);
        return;
    }

    // iOS 18+ workaround
    if (!hapticLabel) {
        hapticLabel = createHapticTrigger();
    }
    hapticLabel.click();
}

// Usage: Haptic on successful task submission
async function submitTask() {
    try {
        await postTask(data);
        triggerHaptic();
        showSuccess('Task submitted');
    } catch (err) {
        showError(err.message);
    }
}
```

**Note:** Haptic feedback is a progressive enhancement. The UI must work correctly without it.

### Pull-to-Refresh

Native-feeling refresh on mobile:

```javascript
let pullStartY = 0;
let isPulling = false;
const PULL_THRESHOLD = 80;

document.addEventListener('touchstart', (e) => {
    if (window.scrollY === 0) {
        pullStartY = e.touches[0].clientY;
        isPulling = true;
    }
});

document.addEventListener('touchmove', (e) => {
    if (!isPulling) return;

    const pullDistance = e.touches[0].clientY - pullStartY;
    if (pullDistance > 0 && pullDistance < PULL_THRESHOLD * 2) {
        // Show pull indicator
        updatePullIndicator(pullDistance / PULL_THRESHOLD);
    }
});

document.addEventListener('touchend', (e) => {
    if (!isPulling) return;
    isPulling = false;

    const pullDistance = e.changedTouches[0].clientY - pullStartY;
    if (pullDistance > PULL_THRESHOLD) {
        triggerRefresh();
    }
    hidePullIndicator();
});
```

---

## Enhanced Accessibility

For WCAG 2.2 AA details (live regions, focus traps, skip links, reduced motion), follow standard WAI-ARIA patterns and mirror the implementation in `internal/view/web/templates/dashboard.html`.

## Error Handling & Recovery

### Error Categories

| Category | HTTP Status | User Message | Recovery Action |
|----------|-------------|--------------|-----------------|
| Network Error | N/A | "Unable to connect" | Auto-retry with backoff |
| Server Error | 5xx | "Server error" | Auto-retry with backoff |
| Auth Error | 401 | "Session expired" | Redirect to login |
| Validation | 400 | Show server message | User corrects input |
| Not Found | 404 | "Not found" | Remove from local state |
| Conflict | 409 | "Agent is busy" | Refresh and prompt user |

### Exponential Backoff for Reconnection

```javascript
class ConnectionManager {
    constructor() {
        this.retryCount = 0;
        this.maxRetries = 10;
        this.baseDelay = 1000;  // 1 second
        this.maxDelay = 60000;  // 1 minute
        this.isConnected = true;
    }

    calculateDelay() {
        // Exponential backoff with jitter
        const exponential = Math.min(
            this.maxDelay,
            this.baseDelay * Math.pow(2, this.retryCount)
        );
        // Add random jitter (0-50% of delay)
        const jitter = exponential * Math.random() * 0.5;
        return exponential + jitter;
    }

    async reconnect() {
        if (this.retryCount >= this.maxRetries) {
            this.showPermanentError();
            return;
        }

        const delay = this.calculateDelay();
        this.showReconnecting(delay);

        await sleep(delay);

        try {
            await this.healthCheck();
            this.retryCount = 0;
            this.isConnected = true;
            this.showConnected();
        } catch (err) {
            this.retryCount++;
            this.reconnect(); // Recursive retry
        }
    }

    onError(err) {
        if (err.name === 'TypeError' || !navigator.onLine) {
            // Network error
            this.isConnected = false;
            this.reconnect();
        }
    }
}
```

### User-Facing Error States

```html
<!-- Connection banner -->
<div x-show="!isConnected" class="connection-banner connection-banner--error">
    <span class="connection-banner__icon">⚠</span>
    <span class="connection-banner__message">
        Connection lost. Reconnecting in <span x-text="reconnectIn"></span>s...
    </span>
    <button @click="retryNow()" class="connection-banner__retry">Retry Now</button>
</div>

<!-- Inline error for task submission -->
<div x-show="submitError" class="form-error" role="alert">
    <span x-text="submitError"></span>
</div>
```

### Graceful Degradation

When features fail, the UI should degrade gracefully:

| Feature | Failure Mode | Degradation |
|---------|--------------|-------------|
| Fleet status | Agent unreachable | Show "Unknown" state, gray icon |
| Task output | Polling fails | Show last known output, "Update paused" |
| Session list | API error | Show cached list, "Last updated X ago" |
| Task submission | Network error | Queue locally, retry when online |
| ETag cache | Header missing | Fall back to full fetch |

---

## Connection Management

### Online/Offline Detection

```javascript
function setupConnectionMonitoring() {
    window.addEventListener('online', () => {
        console.log('Connection restored');
        announceStatus('Connection restored');
        startPolling();
        refresh();
    });

    window.addEventListener('offline', () => {
        console.log('Connection lost');
        announceAlert('Connection lost');
        stopPolling();
        showOfflineBanner();
    });

    // Also detect via failed requests
    // navigator.onLine is not always reliable
}
```

### Health Check Endpoint

The UI should verify connectivity via a lightweight health check:

```javascript
async function healthCheck() {
    const response = await fetchWithTimeout('/api/health', {}, 5000);
    if (!response.ok) {
        throw new Error('Health check failed');
    }
    return true;
}
```

Server implementation:

```go
func (h *Handlers) HandleHealth(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Cache-Control", "no-store")
    w.WriteHeader(http.StatusOK)
    w.Write([]byte("OK"))
}
```

---

## Alpine.js Lifecycle Management

### Cleanup on Component Destruction

```javascript
Alpine.data('dashboard', () => ({
    pollTimer: null,
    abortController: null,

    init() {
        this.startPolling();
        this.setupVisibilityHandler();
    },

    destroy() {
        // Critical: Clean up to prevent memory leaks
        this.stopPolling();
        this.removeVisibilityHandler();

        if (this.abortController) {
            this.abortController.abort();
            this.abortController = null;
        }
    },

    visibilityHandler: null,

    setupVisibilityHandler() {
        this.visibilityHandler = () => {
            if (document.hidden) {
                this.stopPolling();
            } else {
                this.startPolling();
            }
        };
        document.addEventListener('visibilitychange', this.visibilityHandler);
    },

    removeVisibilityHandler() {
        if (this.visibilityHandler) {
            document.removeEventListener('visibilitychange', this.visibilityHandler);
            this.visibilityHandler = null;
        }
    }
}));
```

### Recursive setTimeout vs setInterval

**Recommendation:** Use recursive `setTimeout` instead of `setInterval` for polling.

```javascript
// Preferred: Recursive setTimeout
async function poll() {
    try {
        await refresh();
    } catch (err) {
        handleError(err);
    }

    // Only schedule next poll after current one completes
    if (isPolling) {
        pollTimer = setTimeout(poll, getInterval());
    }
}

// Avoid: setInterval
// - Continues firing even if previous request hasn't completed
// - Can cause request pileup on slow networks
// - Harder to adjust interval dynamically
```

**Benefits of recursive setTimeout:**
- Guarantees minimum interval between request completion and next request start
- Naturally handles slow responses without request pileup
- Easy to adjust interval based on state (active task vs idle)

---

## Placeholder Features (Pending Observability Improvements)

The following features are designed but require observability improvements before full implementation.
They should be integrated into the existing dashboard UI with progressive disclosure:
panels are collapsed by default and only fetch data when the user expands or requests them.

### 1. Token Usage & Cost Display

**Current state:** Placeholder in Task Detail view.

```javascript
// TODO: Requires agent to expose token counts in task response
// Currently shows "—" for tokens_used and cost_usd
function formatMetrics(task) {
    return {
        tokens: task.tokens_used ? formatNumber(task.tokens_used) : '—',
        cost: task.cost_usd ? `$${task.cost_usd.toFixed(3)}` : '—',
        duration: formatDuration(task.duration_seconds),
        model: task.model || '—'
    };
}
```

**Dependency:** Agent must track and report token usage from Claude API responses.

### 2. Step/Trace Visualization

**Current state:** Placeholder section in expanded task view.

```html
<!-- TODO: Requires structured trace data from agent -->
<div class="task-steps" x-show="task.steps && task.steps.length > 0">
    <h4>Steps</h4>
    <!-- Step timeline will go here -->
    <p class="placeholder-text">Step visualization coming soon</p>
</div>
```

**Dependency:** Agent must emit structured step events with timing data.

### 3. Real-time Output Streaming

**Current state:** Polling every 1s for partial output.

**Future improvement:** When observability layer supports it, switch to SSE for true streaming:

```javascript
// Future: SSE-based streaming (when available)
function streamTaskOutput(taskId, onChunk) {
    const eventSource = new EventSource(`/api/task/${taskId}/stream`);
    eventSource.onmessage = (event) => {
        onChunk(event.data);
    };
    eventSource.onerror = () => {
        eventSource.close();
        // Fall back to polling
        pollTaskOutput(taskId, onChunk);
    };
    return eventSource;
}
```

---

## Related Documents

- [PLAN.md](PLAN.md) - Project roadmap
- [DESIGN.md](DESIGN.md) - Technical architecture
- [OBSERVABILITY_UI_MOCKUP.md](OBSERVABILITY_UI_MOCKUP.md) - Stage 2 UI sketch
- [authentication.md](authentication.md) - Auth system

## References

### Web APIs
- [Page Visibility API - MDN](https://developer.mozilla.org/en-US/docs/Web/API/Page_Visibility_API)
- [AbortController - MDN](https://developer.mozilla.org/en-US/docs/Web/API/AbortController)
- [ETag - MDN](https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/ETag)
- [env() Safe Area - MDN](https://developer.mozilla.org/en-US/docs/Web/CSS/env)

### Accessibility
- [ARIA Live Regions - UXPin](https://www.uxpin.com/studio/blog/aria-live-regions-for-dynamic-content/)
- [WCAG 4.1.3 Status Messages](https://wcag.dock.codes/documentation/wcag413/)
- [Accessible Notifications - Sara Soueidan](https://www.sarasoueidan.com/blog/accessible-notifications-with-aria-live-regions-part-2/)

### Patterns
- [Optimistic UI - TanStack Query](https://tanstack.com/query/latest/docs/framework/react/guides/optimistic-updates)
- [Exponential Backoff - AWS](https://docs.aws.amazon.com/prescriptive-guidance/latest/cloud-design-patterns/retry-backoff.html)
- [Alpine.js Polling Patterns](https://khalidabuhakmeh.com/alpinejs-polling-aspnet-core-apis-for-updates)
