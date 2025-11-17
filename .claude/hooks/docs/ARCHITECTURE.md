# Quality Gate Architecture

## System Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          CLAUDE CODE QUALITY GATES                          │
│                          Zero-Tolerance Enforcement                          │
└─────────────────────────────────────────────────────────────────────────────┘

                                    USER REQUEST
                                         │
                                         ↓
┌────────────────────────────────────────────────────────────────────────────┐
│                              CLAUDE PROCESSES REQUEST                       │
└────────────────────────────────────────────────────────────────────────────┘
                                         │
                                         ↓
                            ┌────────────────────────┐
                            │  Makes Code Changes    │
                            │  (Edit/Write tools)    │
                            └────────────────────────┘
                                         │
                                         ↓
┌────────────────────────────────────────────────────────────────────────────┐
│                    GATE 1: PostToolUse Hook (Immediate)                     │
├────────────────────────────────────────────────────────────────────────────┤
│  Script: post-code-change.sh                                               │
│  Trigger: After Edit/Write on .go files                                    │
│  Actions:                                                                   │
│    • golangci-lint run <file>         [Lint modified file]                │
│    • go test <package>                [Test affected package]             │
│    • Check critical file warnings     [Business logic detection]          │
│  Exit Codes:                                                               │
│    • 0 = Pass, continue                                                    │
│    • 1 = Show error (non-blocking)    [Provides feedback to Claude]      │
│  Purpose: Early detection, immediate feedback                             │
└────────────────────────────────────────────────────────────────────────────┘
                                         │
                                         ↓
                            ┌────────────────────────┐
                            │  More Code Changes     │
                            │  (Iterative fixes)     │
                            └────────────────────────┘
                                         │
                                         ↓
                    ┌──────────────────────────────────────┐
                    │  Claude Attempts to Complete Task    │
                    │  or Mark Todo as "completed"         │
                    └──────────────────────────────────────┘
                                         │
                                         ↓
┌────────────────────────────────────────────────────────────────────────────┐
│            GATE 2: Stop/SubagentStop Hook (Quality Gate)                   │
├────────────────────────────────────────────────────────────────────────────┤
│  Script: enforce-quality-gate.sh → pre-completion-validator.sh             │
│  Trigger: Before Claude completes response or task                        │
│  Actions:                                                                   │
│    1. Check if code/workflows modified   [git diff check]                │
│    2. If modified:                                                         │
│       • golangci-lint run ./...        [Full codebase lint]              │
│       • make test-unit                 [All unit tests]                  │
│       • act --dryrun on workflows      [Workflow validation]             │
│    3. If not modified: Skip (performance)                                 │
│  Exit Codes:                                                               │
│    • 0 = All pass, ALLOW completion                                       │
│    • 2 = Any fail, BLOCK completion    [⛔ CANNOT PROCEED]               │
│  Purpose: STRICT QUALITY GATE - No completion without 100% success        │
└────────────────────────────────────────────────────────────────────────────┘
                                         │
                                    ┌────┴────┐
                                    ↓         ↓
                              ┌─────────┐  ┌──────────┐
                              │ PASS ✅ │  │ FAIL ❌  │
                              └─────────┘  └──────────┘
                                    │            │
                                    │            ↓
                                    │    ┌──────────────────────────────┐
                                    │    │  BLOCKED - Exit Code 2       │
                                    │    │  Claude sees error message:  │
                                    │    │  "VALIDATION FAILED"         │
                                    │    │  "YOU CANNOT CLAIM SUCCESS"  │
                                    │    │  "FIX ALL ISSUES"            │
                                    │    └──────────────────────────────┘
                                    │            │
                                    │            ↓
                                    │    ┌──────────────────────────────┐
                                    │    │  Claude Must Fix Errors      │
                                    │    │  Cannot complete task        │
                                    │    │  Cannot mark todos complete  │
                                    │    └──────────────────────────────┘
                                    │            │
                                    │            ↓
                                    │    ┌──────────────────────────────┐
                                    │    │  Retry Loop                  │
                                    │    │  Fix → Re-validate           │
                                    │    └──────────────────────────────┘
                                    │            │
                                    │            ↓
                                    └────────────┘
                                         │
                                         ↓
┌────────────────────────────────────────────────────────────────────────────┐
│               GATE 3: UserPromptSubmit Hook (Final Safety)                 │
├────────────────────────────────────────────────────────────────────────────┤
│  Script: pre-user-prompt-submit.sh                                         │
│  Trigger: Before sending response to user                                 │
│  Actions:                                                                   │
│    • golangci-lint --fast on modified   [Quick lint]                     │
│    • go test -short on packages         [Quick tests]                    │
│    • Critical file warnings             [Final checks]                   │
│  Exit Codes:                                                               │
│    • 0 = Pass, send response                                              │
│    • 1 = Block, show error                                                │
│  Purpose: Final safety net before user sees result                        │
└────────────────────────────────────────────────────────────────────────────┘
                                         │
                                         ↓
                            ┌────────────────────────┐
                            │  RESPONSE SENT TO USER │
                            │  ✅ All gates passed   │
                            └────────────────────────┘
```

## Hook Execution Matrix

| Hook Event | Script | Triggers On | Exit 0 | Exit 1 | Exit 2 | Timeout |
|------------|--------|-------------|---------|---------|---------|----------|
| **PostToolUse** | `post-code-change.sh` | Edit/Write .go files | Continue | Show error, continue | N/A | 300s |
| **Stop** | `enforce-quality-gate.sh` | Claude tries to finish | Allow completion | N/A | **BLOCK** completion | 600s |
| **SubagentStop** | `enforce-quality-gate.sh` | Subagent tries to finish | Allow completion | N/A | **BLOCK** completion | 600s |
| **UserPromptSubmit** | `pre-user-prompt-submit.sh` | Before sending response | Send response | Block response | N/A | 300s |

## Validation Dependency Tree

```
pre-completion-validator.sh (MASTER VALIDATOR)
├── Step 1: Linting
│   ├── Command: golangci-lint run --timeout 5m ./...
│   ├── Requirement: Exit code 0
│   └── On Failure: Set VALIDATION_FAILED=1
│
├── Step 2: Unit Tests
│   ├── Command: make test-unit
│   ├── Requirement: 100% pass rate (exit 0)
│   └── On Failure: Set VALIDATION_FAILED=1
│
├── Step 3: Workflow Validation
│   ├── Command: act --dryrun (per workflow file)
│   ├── Requirement: Valid YAML syntax
│   └── On Failure: Set VALIDATION_FAILED=1
│
└── Final Decision
    ├── If VALIDATION_FAILED=1:
    │   ├── Print comprehensive error message
    │   ├── Print fix instructions
    │   ├── Write to stderr
    │   └── Exit 2 (BLOCKS COMPLETION)
    │
    └── If VALIDATION_FAILED=0:
        ├── Print success message
        ├── Output JSON with decision: null
        └── Exit 0 (ALLOWS COMPLETION)
```

## Configuration Flow

```
.claude/settings.local.json
│
├── hooks
│   │
│   ├── PostToolUse
│   │   └── matcher: "Edit|Write"
│   │       └── command: post-code-change.sh
│   │           └── timeout: 300s
│   │
│   ├── Stop
│   │   └── command: enforce-quality-gate.sh
│   │       └── timeout: 600s
│   │       └── calls: pre-completion-validator.sh
│   │
│   ├── SubagentStop
│   │   └── command: enforce-quality-gate.sh
│   │       └── timeout: 600s
│   │       └── calls: pre-completion-validator.sh
│   │
│   └── UserPromptSubmit
│       └── command: pre-user-prompt-submit.sh
│           └── timeout: 300s
│
└── permissions
    ├── allow: [test commands, lint commands, etc.]
    └── defaultMode: "acceptEdits"
```

## State Machine Diagram

```
                        ┌─────────────────┐
                        │  IDLE STATE     │
                        │  (No changes)   │
                        └────────┬────────┘
                                 │
                         User gives request
                                 │
                                 ↓
                        ┌─────────────────┐
                        │  CODING STATE   │
                        │  Claude working │
                        └────────┬────────┘
                                 │
                     PostToolUse hook fires
                                 │
                        ┌────────┴─────────┐
                        │                  │
                   Tests Pass         Tests Fail
                        │                  │
                        ↓                  ↓
                ┌──────────────┐    ┌─────────────┐
                │ CONTINUE     │    │ SHOW ERROR  │
                │ CODING       │    │ TO CLAUDE   │
                └──────┬───────┘    └──────┬──────┘
                       │                   │
                       │                   ↓
                       │            ┌──────────────┐
                       │            │ Claude Fixes │
                       │            │ Issues       │
                       │            └──────┬───────┘
                       │                   │
                       └───────────────────┘
                                 │
                     Claude attempts completion
                                 │
                                 ↓
                        ┌─────────────────┐
                        │ VALIDATION      │
                        │ STATE           │
                        └────────┬────────┘
                                 │
                     Stop hook fires
                     (enforce-quality-gate.sh)
                                 │
                        ┌────────┴─────────┐
                        │                  │
                   All Pass           Any Fail
                    (Exit 0)           (Exit 2)
                        │                  │
                        ↓                  ↓
                ┌──────────────┐    ┌─────────────┐
                │ COMPLETED    │    │ BLOCKED     │
                │ STATE        │    │ STATE       │
                └──────┬───────┘    └──────┬──────┘
                       │                   │
                       │                   ↓
                       │            ┌──────────────────┐
                       │            │ Cannot complete  │
                       │            │ Cannot mark todo │
                       │            │ Must fix errors  │
                       │            └──────┬───────────┘
                       │                   │
                       │                   ↓
                       │            ┌──────────────────┐
                       │            │ FIXING STATE     │
                       │            │ Claude debugging │
                       │            └──────┬───────────┘
                       │                   │
                       │                   ↓
                       │            Return to CODING
                       │                   │
                       │                   │
                       └───────────────────┘
                                 │
                    UserPromptSubmit hook
                                 │
                        ┌────────┴─────────┐
                        │                  │
                   Pass (Exit 0)      Fail (Exit 1)
                        │                  │
                        ↓                  ↓
                ┌──────────────┐    ┌─────────────┐
                │ SEND TO USER │    │ BLOCK SEND  │
                │ ✅ Success   │    │ ❌ Blocked  │
                └──────────────┘    └─────────────┘
```

## Component Interaction Diagram

```
┌───────────────────────────────────────────────────────────────────────┐
│                         CLAUDE CODE (AI Agent)                        │
└─────┬──────────────────────────────────────┬──────────────────────────┘
      │                                      │
      │ Uses tools                           │ Attempts completion
      │                                      │
      ↓                                      ↓
┌─────────────┐                      ┌──────────────┐
│ Edit Tool   │                      │ Stop Event   │
│ Write Tool  │                      │ SubagentStop │
└──────┬──────┘                      └──────┬───────┘
       │                                    │
       │ Triggers                           │ Triggers
       ↓                                    ↓
┌──────────────────┐              ┌─────────────────────┐
│ PostToolUse Hook │              │ Stop/SubagentStop   │
└────────┬─────────┘              │ Hook                │
         │                        └──────────┬──────────┘
         │                                   │
         ↓                                   ↓
┌─────────────────────┐           ┌──────────────────────┐
│ post-code-change.sh │           │ enforce-quality-     │
│                     │           │ gate.sh              │
│ • Lint file         │           └──────────┬───────────┘
│ • Test package      │                      │
│ • Show warnings     │                      │ Delegates
└────────┬────────────┘                      ↓
         │                        ┌──────────────────────┐
         │ Exit 0/1               │ pre-completion-      │
         │                        │ validator.sh         │
         ↓                        │                      │
┌─────────────────────┐           │ • Lint all           │
│ Shows to Claude     │           │ • Test all           │
│ (feedback)          │           │ • Validate workflows │
└─────────────────────┘           └──────────┬───────────┘
                                             │
                                             │ Exit 0/2
                                             ↓
                                  ┌──────────────────────┐
                                  │ If Exit 2:           │
                                  │ BLOCKS completion    │
                                  │ Shows error to Claude│
                                  │                      │
                                  │ If Exit 0:           │
                                  │ Allows completion    │
                                  └──────────┬───────────┘
                                             │
                                             ↓
                                  ┌──────────────────────┐
                                  │ UserPromptSubmit     │
                                  │ Hook (Final check)   │
                                  └──────────┬───────────┘
                                             │
                                             ↓
                                  ┌──────────────────────┐
                                  │ Response to User     │
                                  └──────────────────────┘
```

## Data Flow: Validation Failure

```
1. Code Change Made
   └─→ PostToolUse: post-code-change.sh
       └─→ Test Fails
           └─→ Exit 1 (non-blocking)
               └─→ stderr: "Tests failed in package X"
                   └─→ Claude sees: "❌ FAILED: Tests failed in X"

2. Claude Attempts Fix
   └─→ Makes more changes
       └─→ PostToolUse: post-code-change.sh
           └─→ Still failing
               └─→ Exit 1 again

3. Claude Thinks It's Fixed (but it's not)
   └─→ Tries to complete task
       └─→ Stop: enforce-quality-gate.sh
           └─→ Detects changes
               └─→ Calls: pre-completion-validator.sh
                   └─→ Runs: make test-unit
                       └─→ Tests FAIL ❌
                           └─→ VALIDATION_FAILED=1
                               └─→ Exit 2 (BLOCKING)
                                   └─→ stderr: "VALIDATION FAILED: Cannot complete task"
                                       └─→ Claude BLOCKED, cannot proceed
                                           └─→ Must fix actual issue
                                               └─→ Retry loop begins

4. Claude Fixes Real Issue
   └─→ Makes correct fix
       └─→ PostToolUse: Tests pass ✅
           └─→ Tries completion again
               └─→ Stop: enforce-quality-gate.sh
                   └─→ pre-completion-validator.sh
                       └─→ All tests pass ✅
                           └─→ All linting passes ✅
                               └─→ Workflows valid ✅
                                   └─→ Exit 0
                                       └─→ Completion ALLOWED
                                           └─→ Task marked complete ✅
```

## Security Model

```
┌─────────────────────────────────────────────────────────────┐
│                    TRUST BOUNDARIES                          │
└─────────────────────────────────────────────────────────────┘

User Trust Boundary
├── User requests task from Claude
└── User expects quality guarantees

    ↓

Quality Gate Enforcement (Zero Trust for Claude)
├── Claude claims completion
├── VERIFY: All tests pass
├── VERIFY: All linting passes
├── VERIFY: All workflows valid
├── REJECT: If any verification fails
└── ACCEPT: Only if all verifications pass

    ↓

System Trust Boundary
├── System runs validation commands
│   ├── make test-unit
│   ├── golangci-lint
│   └── act --dryrun
├── System evaluates exit codes
└── System enforces blocking (exit 2)

    ↓

Result Delivered to User
└── Only if all gates passed
```

## Performance Characteristics

| Hook | Execution Time | Performance Impact | Optimization |
|------|----------------|-------------------|--------------|
| PostToolUse | ~10-30s | Per file change | Quick tests on affected package only |
| Stop/SubagentStop | ~60-300s | Per completion attempt | Only runs if changes detected |
| UserPromptSubmit | ~10-60s | Per response | Fast mode linting, short tests |
| Workflow Validation | ~5-30s | Per workflow file | Dry-run mode (no execution) |

**Total Overhead:**
- Clean state (no changes): ~0s (gates bypassed)
- With changes: ~100-400s (full validation)
- Per code change: ~10-30s (immediate feedback)

## Error Recovery Flow

```
Error Detected
    │
    ↓
┌─────────────────────────┐
│ Hook Returns Exit 2     │
│ (Blocking error)        │
└───────────┬─────────────┘
            │
            ↓
┌─────────────────────────┐
│ Error Written to stderr │
│ Formatted message       │
└───────────┬─────────────┘
            │
            ↓
┌─────────────────────────┐
│ Claude Receives Error   │
│ Cannot proceed          │
└───────────┬─────────────┘
            │
            ↓
┌─────────────────────────┐
│ Claude Analyzes Error   │
│ Reads failure details   │
└───────────┬─────────────┘
            │
            ↓
┌─────────────────────────┐
│ Claude Makes Fix        │
│ Edits code              │
└───────────┬─────────────┘
            │
            ↓
┌─────────────────────────┐
│ PostToolUse Hook        │
│ Validates fix           │
└───────────┬─────────────┘
            │
            ├─→ Still Failing → Loop back
            │
            └─→ Now Passing → Continue
                    │
                    ↓
            ┌─────────────────────────┐
            │ Attempt Completion      │
            └───────────┬─────────────┘
                        │
                        ↓
            ┌─────────────────────────┐
            │ Stop Hook Validates     │
            └───────────┬─────────────┘
                        │
                        └─→ Success ✅
```

## Key Architectural Principles

1. **Defense in Depth**: Multiple validation layers
2. **Fail-Safe**: Blocks by default when validation fails
3. **Clear Feedback**: Detailed error messages guide fixes
4. **Performance Optimized**: Smart validation triggers
5. **Zero Trust**: Claude must prove success, cannot claim it
6. **Deterministic**: Same code state always produces same result
7. **Comprehensive**: Covers linting, tests, and workflows
8. **Maintainable**: Modular scripts, clear configuration
