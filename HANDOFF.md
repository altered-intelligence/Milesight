# HANDOFF — read this first

Milesight is built across many sessions by different people and models. This file is the
entry point. It defines **how** to hand off, and holds the **current snapshot**. The
item-level truth lives in [`docs/handoff/LEDGER.md`](docs/handoff/LEDGER.md).

## 1. Protocol

### Core rule — the delta is incomplete work
Every work item moves through **planned → implemented → tested → verified**. Treat
anything that has not reached **TESTED** (or **VERIFIED**) as **incomplete**, no matter
what a summary, commit message, or previous session claims. The gap between what was
planned, what was written, and what was proven is exactly the backlog for the next session.

### Status legend (LEDGER.md)
| Status | Meaning |
|---|---|
| `PLANNED` | Intended; no code yet. |
| `WIP` | Partially written; may not compile. Must be finished or reverted. |
| `IMPLEMENTED` | Code complete and compiles; **no automated proof** of behavior. Incomplete. |
| `TESTED` | Automated tests exercise it and pass. Evidence column names the tests/command. |
| `VERIFIED` | Exercised end-to-end in a running system (e.g. live server + client). |
| `BLOCKED` | Needs something outside the repo (account, credential, decision). Says what. |
| `BACKLOG` | Known future work, not scheduled. |
| `DROPPED` | Decided against; links the ADR. |

iOS code cannot be compiled in Linux sandboxes. iOS items stay at `IMPLEMENTED` until a
macOS build (CI or Xcode) succeeds; record the run as evidence.

### Start of a session (do these before writing code)
1. Read this file, then `LEDGER.md`, then the newest file in `docs/handoff/sessions/`.
2. Re-run the evidence for anything you will build on (`docs/handoff/ENVIRONMENT.md` has
   the commands). If it fails, **downgrade** the ledger row and note why. Trust, but verify.
3. Write your session plan into a new session log (copy the template below) **before**
   starting, listing ledger IDs you intend to move.

### During the session
- Add a ledger row for any new work item before starting it (status `PLANNED`/`WIP`).
- Update rows as items change status; evidence must be concrete (test names, commands,
  observed output), never "should work".
- Record decisions as ADRs in `docs/decisions.md` and link them from the ledger.
- Commit in small, buildable steps. Never leave `main` not compiling; use `WIP` rows and a
  branch if you must stop mid-change.

### End of a session (or when the user asks for a handoff)
1. Run the full test suite; paste the summary into your session log.
2. Reconcile: for every item in your session plan, set its final ledger status. Anything
   you planned but did not finish stays `PLANNED`/`WIP`/`IMPLEMENTED` with a note.
3. Fill in the session log: planned vs. done vs. tested, surprises, open questions.
4. Update §2 and §3 below (snapshot + next steps). Keep them short.
5. Commit and push. If you cannot push, say so in the snapshot and hand the user a bundle.

### Session log template (`docs/handoff/sessions/S<NN>-<date>-<agent>.md`)
```
# S<NN> — <date> — <agent/model>
## Plan (ledger IDs)
## Done (with status reached + evidence)
## Not done / partially done (why)
## Test run summary
## Decisions (ADR links)
## Surprises, risks, open questions
## Exact next step
```

## 2. Current snapshot
_Last updated: S01, 2026-10-14, Claude Opus 5.5 (in progress)._

- **Shape:** native iOS app (SwiftUI) + Go backend; Android later; web dashboard optional.
- **Backend:** being rebuilt from stubs into a working service. Stores (memory + Postgres),
  auth, Tesla Fleet/Owner transports, simulator, and analytics are written and **TESTED**.
  Ingestion, notifications, commands and the HTTP/WebSocket API are next (see ledger).
- **iOS:** still the original scaffold; client and screens not yet updated to the new API.
- **Repo state:** GitHub is not linked to the S01 workspace, so S01 work is committed
  locally and delivered as a git bundle; it is not on GitHub until someone pushes it.

## 3. Exact next steps
See the newest session log for the authoritative list. At the time of writing:
1. Finish backend: realtime hub, ingestion manager, notifications/APNs, commands, API, server main.
2. End-to-end API test (simulator + memory store) and live smoke test incl. WebSocket.
3. Update iOS kits/screens to the contract in `docs/api.md`; get them compiling in CI.

## 4. Where things are
- `docs/handoff/LEDGER.md` — every work item, status, evidence.
- `docs/handoff/sessions/` — one log per session.
- `docs/handoff/ENVIRONMENT.md` — build/test commands and sandbox quirks.
- `docs/decisions.md` — ADRs. `docs/api.md` — API contract. `docs/plan.md` — product plan.
