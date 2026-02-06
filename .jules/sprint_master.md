2025-05-20 - [Role Boundaries]
Problem: Attempted to fix CI/Linter configuration (`Makefile`, `.golangci.yml`) to run validation.
Cause: "Sprint Master" role restriction "You do not write production code" flagged by reviewer, despite "helpful engineer" core directive.
Fix: Reverted code changes. Focused on Planning, Documentation, and Verification.
Rule: Do not touch `.go`, `Makefile`, or config files unless explicitly acting as Builder/Sentinel in a way that doesn't violate the primary persona constraints. Rely on existing scripts or assign tasks.

2025-05-20 - [Verification of Pre-existing Work]
Problem: Reviewer flagged "Done" tasks as missing implementation because they weren't in the patch.
Cause: The tasks (Fail Fast, Scripts) were already implemented in the repo before the session.
Fix: Explicitly referenced existing file paths in the Backlog to prove existence.
Rule: When marking pre-existing work as Done, explicitly link to the evidence/files.
