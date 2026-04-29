# cf-vpn Control Panel UI Redesign — Design

**Status:** Draft for review  
**Date:** 2026-04-22  
**Scope:** UI/UX redesign only (no backend/API behavior changes)

## Goal

Redesign the existing cf-vpn control panel UI to improve daily operations speed for VPS and user management, with a Network Operations (NOC) visual style.

## Constraints

- UI-only redesign; do not change backend contracts or core behavior.
- Keep all current core capabilities available.
- Optimize for both desktop and mobile from the start.
- Prioritize operator workflows over visual novelty.

## Top-priority workflows

1. Check VPS status quickly (up/down/degraded, latency, last seen).
2. Rotate domain per node with clear feedback.
3. Retrieve user subscription quickly (copy text + QR).

## Information architecture

Primary navigation is simplified from the current structure into:

1. **Command Center** (default): node operations-first workspace.
2. **Users**: user-centric management and subscription retrieval.
3. **Events**: audit/event browsing for troubleshooting.

Secondary destination:

- **Settings** is moved to a header icon/action (not top-level), because it is lower-frequency.

## Command Center layout

### Desktop

Three-zone layout:

1. **Top bar (sticky)**
   - Environment/context label
   - Global search (node/user)
   - Quick Add actions
   - Settings access

2. **Status strip**
   - Active count
   - Degraded count
   - Down count
   - Average latency
   - Each KPI acts as a quick filter on node list

3. **Main workspace**
   - **Node card grid** (2–4 columns depending on width)
   - **Quick User Panel** on the right (sticky)
     - Recent users
     - User search
     - 1-click Copy Subscription
     - 1-click Show QR

### Mobile

Stacked layout with contextual overlays:

- Compact horizontal status pills on top.
- Single-column node cards with primary action visible.
- Floating action button opens **Users bottom sheet**.
- Node detail opens full-screen sheet and returns to prior scroll position.

## Node card specification

Each node card shows, in priority order:

1. **Operational health:** status dot + latency.
2. **Current endpoint:** VPN host + last seen.
3. **Primary actions:**
   - Rotate (primary)
   - Healthcheck
   - Open detail

Behavioral states:

- Loading: skeleton/disabled actions.
- Success: inline timestamp refresh + toast.
- Partial/error: warning badge + CTA to Events/retry.
- Stale/offline: data remains visible with stale indicator.

## Visual direction (NOC)

- Dark operations-focused background (charcoal/navy).
- High-contrast state colors:
  - Active: green
  - Degraded: amber
  - Down: red
  - Unknown: cool gray
- Medium-high information density on desktop, preserved touch targets on mobile.

## Interaction flows

### 1) Status-first inspection

- Open Command Center and immediately view fleet health summary.
- Filter by status via KPI strip.
- Open node detail drawer/sheet without losing grid context.

### 2) Rotate node domain

- Trigger Rotate directly from node card.
- Confirm in compact modal.
- Node enters Rotating state (temporary action lock).
- On success: host updates inline + success toast + optional “Copy updated subscription” CTA.
- On failure: node badge marks failure + “View event” and retry actions.

### 3) Retrieve subscription/QR

- Use Quick User Panel from Command Center (no full navigation required).
- Select/search user, then:
  - Copy Subscription (primary)
  - Show QR (secondary prominent)
- QR shown in modal/sheet with copy action below.

## Acceptance criteria (UI redesign)

1. Operator can see full fleet health summary within ~3 seconds of opening panel.
2. Rotate for one node can be completed in at most 3 interaction steps.
3. Copy subscription or open QR for one user can be completed in at most 3 interaction steps.
4. Both desktop and mobile preserve all 3 top workflows without deep navigation detours.
5. Error/partial states are visible in context with immediate next actions.

## Manual validation scenarios

1. Open app, filter degraded nodes, open node detail, return to preserved grid context.
2. Rotate SG node and observe rotating state, success feedback, and updated host on card.
3. From Command Center, open user panel, copy subscription, then open QR.
4. Repeat critical flows on mobile using bottom sheet/FAB patterns.

## Out of scope

- Backend/API redesign.
- New product features beyond current scope.
- Multi-tenant/RBAC redesign.
- Realtime streaming dashboards beyond current behavior.
