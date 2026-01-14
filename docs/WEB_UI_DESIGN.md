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
│   ├── Context picker
│   ├── Prompt input
│   └── Options (model, timeout, thinking)
│
└── Settings
    ├── Paired devices
    ├── Contexts management
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
│  Context                                                │
│  ┌─────────────────────────────────────────────────┐   │
│  │ None (custom prompt)                         ▼  │   │
│  └─────────────────────────────────────────────────┘   │
│                                                         │
│  Prompt                                                 │
│  ┌─────────────────────────────────────────────────┐   │
│  │                                                 │   │
│  │                                                 │   │
│  │                                                 │   │
│  └─────────────────────────────────────────────────┘   │
│                                                         │
│  Options                                           ▼    │
│                                                         │
│  ┌─────────────────────────────────────────────────┐   │
│  │                                      Submit ▶   │   │
│  └─────────────────────────────────────────────────┘   │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

---

## API Changes

### Task Summary Field

To display human-readable summaries instead of cryptic UUIDs, the agent API must be extended.

#### Agent API Changes

**POST /task** - Request unchanged, response extended:

```json
{
  "task_id": "task_a1b2c3d4",
  "session_id": "sess_e5f6g7h8",
  "summary": "Fix auth token refresh"  // NEW: 30 char max
}
```

**GET /task/:id** - Response extended:

```json
{
  "id": "task_a1b2c3d4",
  "state": "completed",
  "summary": "Fix auth token refresh",  // NEW
  "prompt": "Fix the authentication token refresh logic...",
  "output": "I'll analyze the auth client code...",
  "exit_code": 0,
  "duration_seconds": 45.2,
  "tokens_used": 12400,      // NEW: optional
  "cost_usd": 0.037          // NEW: optional
}
```

**GET /history** - List includes summaries:

```json
{
  "tasks": [
    {
      "id": "task_a1b2c3d4",
      "summary": "Fix auth token refresh",  // NEW
      "state": "completed",
      "started_at": "2025-01-14T14:23:05Z",
      "completed_at": "2025-01-14T14:23:50Z"
    }
  ]
}
```

#### Summary Generation

The agent generates summaries from the prompt using:

1. **First sentence extraction** - Take first sentence if under 30 chars
2. **Imperative verb detection** - Extract "Fix X", "Add Y", "Update Z" patterns
3. **Truncation with ellipsis** - If still too long, truncate at word boundary + "..."

Implementation pseudocode:

```go
func GenerateSummary(prompt string, maxLen int) string {
    // Try first sentence
    if idx := strings.IndexAny(prompt, ".!?"); idx > 0 && idx < maxLen {
        return strings.TrimSpace(prompt[:idx])
    }

    // Try first line
    if idx := strings.Index(prompt, "\n"); idx > 0 && idx < maxLen {
        return strings.TrimSpace(prompt[:idx])
    }

    // Truncate at word boundary
    if len(prompt) <= maxLen {
        return prompt
    }

    truncated := prompt[:maxLen]
    if idx := strings.LastIndex(truncated, " "); idx > maxLen/2 {
        return truncated[:idx] + "..."
    }
    return truncated[:maxLen-3] + "..."
}
```

#### Session Summary

Sessions use the **first task's summary** as their display name:

```json
{
  "id": "sess_e5f6g7h8",
  "summary": "Fix auth token refresh",  // From first task
  "agent_url": "http://localhost:9001",
  "tasks": [...]
}
```

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

### Final Design

**[mockup-final.html](mockup-final.html)** - The approved design combining:
- **Feature layout** from mockup-basic (sessions, tasks, fleet, task submission)
- **Visual style** from mockup-langsmith (colors, typography, page layout, CSS)
- **Architectural decisions:** Polling, flat hierarchy, no offline

### Design Explorations

Three exploratory mockups were created during design:

1. **[mockup-basic.html](mockup-basic.html)** - Conventional, Bootstrap-inspired layout
2. **[mockup-creative.html](mockup-creative.html)** - Innovative, experimental design
3. **[mockup-langsmith.html](mockup-langsmith.html)** - Inspired by LangSmith observability UI

### Mockup Features

All mockups demonstrate:
- Dark mode with Danish minimalism
- iPhone 17 Pro responsive design
- Expandable session cards (flat, accordion-style)
- Human-readable summaries
- Fleet status panel
- Polling status indicator

---

## Resolved Questions

1. **Real-time updates** - **Polling.** HTTP polling with visibility-based pausing. Simple, reliable, sufficient for dashboard use case.

2. **Offline support** - **None.** Dashboard is for real-time monitoring; cached data would be stale and misleading.

3. **Session hierarchy** - **Flat.** Simple list of sessions, each containing one task. No nested trace trees.

---

## Implementation Plan

### Implementation Decisions

These decisions were made during design review:

#### 1. UI Replacement Strategy: Full Replacement

**Decision:** Complete replacement of existing `dashboard.html` (44KB).

**Rationale:**
- Existing UI has different architecture (server-rendered templates)
- New design uses Alpine.js for reactivity
- Clean slate avoids technical debt from mixing approaches
- Mockup already implements full functionality

**Implication:** Delete `internal/view/web/templates/dashboard.html` and related server-side rendering logic. New UI is a single-page application.

#### 2. Fleet Summary Source: Agents Only

**Decision:** Collapsed fleet panel shows agent counts only (not directors/helpers).

**Rationale:**
- Agents are the primary work executors
- Simpler summary (e.g., "2 idle, 1 working")
- Directors and helpers visible when fleet expanded
- Matches user's mental model of "available workers"

**Implication:** Fleet summary bar displays: `Fleet ▸ 2 idle · 1 working` counting only agents. Full breakdown (agents, directors, helpers) shown when expanded.

#### 3. Output Display: Poll for Partial Output

**Decision:** Poll `/task/:id` every 1s while task is working to show partial output.

**Rationale:**
- Users want to see progress during long-running tasks
- Polling is consistent with overall architecture (no WebSocket)
- Agent already returns partial output in `output` field
- Simple implementation: same endpoint, just display incrementally

**Implication:** When a task is in `working` state, poll every 1s and update the output display. Use CSS animation to indicate streaming. Add "live" indicator while task is active.

#### 4. Frontend Framework: Alpine.js

**Decision:** Use Alpine.js for reactive UI.

**Rationale:**
- Declarative, no build step required
- 15KB minified, loads from CDN
- Simple `x-data`, `x-show`, `x-for` directives
- Easy to understand and modify
- Good fit for dashboard complexity level
- Better DX than vanilla JS for reactive state

**Implication:** Include Alpine.js via CDN. State management via Alpine stores. No build tooling required.

### Implementation Phases

#### Phase 1: Foundation (1 PR)

**Goal:** Replace existing UI with new Alpine.js-based dashboard.

**Tasks:**
1. Create new `dashboard.html` using mockup-final as base
2. Add Alpine.js from CDN
3. Implement API client (`/api/dashboard`, `/api/task`, `/api/agents`)
4. Set up polling infrastructure with visibility API
5. Delete old dashboard.html and related server templates
6. Update handlers.go to serve new SPA

**Files to create:**
- `internal/view/web/templates/dashboard.html` (new, Alpine.js SPA)

**Files to delete:**
- `internal/view/web/templates/dashboard.html` (old, 44KB)
- Related template partials if any

**Files to modify:**
- `internal/view/web/handlers.go` - Simplify to serve SPA
- `internal/view/web/embed.go` - Update embed directives

#### Phase 2: Fleet Panel

**Goal:** Collapsible fleet with agent-only summary.

**Tasks:**
1. Implement collapsed state showing "2 idle · 1 working"
2. Count agents only for summary stats
3. Expanded state shows full breakdown (agents, directors, helpers)
4. Persist collapse state in localStorage
5. Add fleet refresh on interval (5s idle, 2s active)

#### Phase 3: Session Management

**Goal:** Accordion-style session list with flat hierarchy.

**Tasks:**
1. Fetch sessions from `/api/sessions`
2. Implement accordion (single expanded at a time)
3. Session card shows: status dot, summary, agent, timestamp
4. Expanded view shows session metadata and task list
5. Task cards within session (nested accordion)

#### Phase 4: Live Task Output

**Goal:** Poll and display partial output during task execution.

**Tasks:**
1. Detect when a task is in `working` state
2. Poll `/task/:id` every 1s for that task
3. Update output display incrementally
4. Add "live" indicator with pulse animation
5. Stop polling when state changes to completed/failed/cancelled

#### Phase 5: Task Submission

**Goal:** Full-screen modal for submitting new tasks.

**Tasks:**
1. Agent selector dropdown (filter to idle agents)
2. Context picker from contexts.yaml
3. Prompt textarea with markdown preview
4. Options panel (model, timeout, thinking budget)
5. Submit action with optimistic UI update
6. Error handling and validation

#### Phase 6: Polish

**Goal:** Accessibility, keyboard shortcuts, and refinements.

**Tasks:**
1. Keyboard navigation (j/k, Enter, Escape, n, r, f)
2. Focus management for modals
3. ARIA labels and roles
4. Screen reader testing
5. Reduced motion support
6. Error states and loading skeletons

### Migration Notes

**Backward Compatibility:**
- No backward compatibility needed (internal tool)
- Single deployment replaces old UI entirely
- No feature flags or gradual rollout

**Testing Approach:**
1. Manual testing on iPhone 17 Pro (primary target)
2. Desktop browser testing (Chrome, Firefox, Safari)
3. Existing integration tests still pass (API unchanged)
4. New E2E tests for critical flows

**Rollback Plan:**
- Git revert if issues found
- Old dashboard.html preserved in git history

---

## Open Questions

1. **Export/sharing** - Do users need to export session data (JSON, share links)?

2. **Multi-agent views** - When director-claude orchestrates multiple agents, how should the UI visualize the parent-child relationship?

---

## Related Documents

- [PLAN.md](PLAN.md) - Project roadmap
- [DESIGN.md](DESIGN.md) - Technical architecture
- [authentication.md](authentication.md) - Auth system
