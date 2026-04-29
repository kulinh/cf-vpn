# Users Page — Upgrade User to Missing Nodes (Design)

**Status:** Draft for review  
**Date:** 2026-04-23  
**Scope:** Add backend + frontend support for per-user node sync (missing nodes only)

## Goal

Add a per-user **Upgrade** action on the Users page so operators can sync one user into all currently available nodes, while skipping nodes the user already has.

## Confirmed UX decisions

- Action is per-user (not bulk for all users).
- Upgrade requires a confirm step.
- UI should be mobile-optimized.
- If user already has all nodes: show disabled state with **Up-to-date**.

## API design

### Endpoint

- `POST /api/users/{userId}/upgrade-nodes`

### Behavior

1. Load current server-side node inventory.
2. Load target user and current user-node assignments.
3. Compute missing nodes = all nodes - user.nodes.
4. Add only missing nodes.
5. Persist updated user assignment state.
6. Regenerate user subscription payload so newly added nodes are reflected.
7. Return structured result.

### Idempotency

- Repeated calls are safe.
- If no missing nodes exist, return success with `addedCount = 0`.

### Suggested response shape

```json
{
  "userId": "kulinh",
  "addedNodes": ["VN"],
  "addedCount": 1,
  "alreadyPresentCount": 4,
  "totalNodesAfterUpgrade": 5
}
```

### Error behavior

- User not found → 404-style API error.
- Persistence/subscription update failures → error response; no partial success should be reported as success.

## Frontend design (Users page)

## Data dependencies

- Existing: `listUsers()` for user cards.
- Add: `listNodes()` usage in Users page to calculate missing-node count for button state.
- Add: `upgradeUserNodes(userId)` API client helper.

## Per-user card actions

Each user card will keep existing actions and add:

- `Upgrade (+N)` when missing count `N > 0`
- Disabled `Up-to-date` when missing count is `0`

## Confirmation UX (mobile-first)

- On tap/click Upgrade, open confirm UI (dialog/sheet pattern consistent with existing app UX):
  - Title: `Upgrade user <name>?`
  - Body: `Add N missing nodes. Existing nodes are kept.`
  - Buttons: `Cancel` / `Confirm Upgrade`

## Progress and completion states

- On confirm:
  - Lock only that user’s Upgrade button (`Upgrading...`).
  - Prevent duplicate submissions for same user.
- On success with additions:
  - Update that user’s `nodes` in local state immediately using response payload.
  - Recompute missing count.
  - Show success feedback like `Added X nodes`.
- On success with no additions:
  - Keep user state, show `User is already up-to-date`.
- On failure:
  - Keep existing state.
  - Clear loading state.
  - Show short error feedback.

## Data flow

1. Users page loads users and nodes.
2. For each user, compute `missingCount` against current node inventory.
3. User taps Upgrade → confirm shown.
4. Confirm → call `POST /api/users/{id}/upgrade-nodes`.
5. Merge response into local user state.
6. UI recalculates and renders `Up-to-date` when no missing nodes remain.

## Edge cases

- New nodes may be added after page load:
  - Backend remains source of truth for actual upgrade result.
  - Frontend reconciles from endpoint response after each upgrade.
- Rapid repeated taps:
  - Guard with per-user in-flight lock.

## Testing strategy

## Frontend tests (`panel/web/src/pages/UsersPage.test.tsx`)

1. Renders Upgrade action with correct label (`Upgrade (+N)`) when user has missing nodes.
2. Shows disabled `Up-to-date` when user has all nodes.
3. Opens and closes confirmation UI.
4. Confirm triggers `upgradeUserNodes` with correct user ID.
5. Shows per-user loading state during request.
6. Success with `addedCount > 0` updates rendered node list and status.
7. Success with `addedCount = 0` keeps up-to-date state.
8. Failure clears loading and keeps previous node data.

## API client tests (`panel/web/src/lib/api.ts` coverage)

- `upgradeUserNodes` calls expected endpoint and parses response fields.

## Backend tests

1. Adds only missing nodes (no duplicates).
2. No-op success when already up-to-date.
3. Returns appropriate error when user does not exist.
4. Handles persistence or subscription regeneration failures correctly.

## Out of scope

- Bulk upgrade for all users in one click.
- Changing existing user/node domain model beyond sync behavior.
- Redesign of non-Users page flows.
