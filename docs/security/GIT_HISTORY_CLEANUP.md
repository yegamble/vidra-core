# Git History Cleanup Guide

## ⚠️ WARNING: DESTRUCTIVE ACTION

**This process rewrites git history. It is destructive and irreversible.**

If you make a mistake, you can lose code.
**ALWAYS** make a backup of your repository before proceeding.

```bash
cp -r . ../repo-backup-$(date +%s)
```

## Overview

This guide explains how to remove sensitive files (like `.env`, credentials, or large binaries) from the entire git history. This is necessary when secrets have been accidentally committed.

We recommend using [BFG Repo-Cleaner](https://rtyley.github.io/bfg-repo-cleaner/) as it is faster and simpler than `git filter-branch`.

## Prerequisites

1.  **Backup**: As mentioned above.
2.  **Coordination**: Ensure all team members have pushed their valid changes. They will need to **re-clone** the repository after this operation.
3.  **Stop Pipelines**: Pause any CI/CD pipelines to prevent them from failing or deploying broken states.

## Method 1: BFG Repo-Cleaner (Recommended)

1.  **Install BFG**:
    *   macOS: `brew install bfg`
    *   Linux: Download the jar from the [website](https://rtyley.github.io/bfg-repo-cleaner/).

2.  **Run Cleanup**:
    To remove a specific file (e.g., `.env`):
    ```bash
    bfg --delete-files .env
    ```

    To replace text in files (e.g., a specific API key):
    ```bash
    # Create a file containing the text to replace
    echo "MY_SECRET_KEY" > passwords.txt
    bfg --replace-text passwords.txt
    ```

3.  **Clean Reflog & GC**:
    BFG updates the commits but the old objects remain in the git database until garbage collected.
    ```bash
    git reflog expire --expire=now --all && git gc --prune=now --aggressive
    ```

## Method 2: Git Filter-Branch (Standard)

If you cannot install BFG, use the built-in `git filter-branch`. **Note:** This is much slower.

1.  **Run Filter**:
    ```bash
    git filter-branch --force --index-filter \
    "git rm --cached --ignore-unmatch .env" \
    --prune-empty --tag-name-filter cat -- --all
    ```

2.  **Clean Reflog & GC**:
    ```bash
    rm -rf .git/refs/original/
    git reflog expire --expire=now --all
    git gc --prune=now --aggressive
    ```

## Final Step: Force Push

Once you have verified that the history is clean and the file is gone:

```bash
git push origin --force --all
git push origin --force --tags
```

## Aftermath

1.  **Notify Team**: Tell all developers to delete their local repo and re-clone.
    *   *Do not* try to pull/merge the new history into an old clone; it will re-introduce the dirty history.
2.  **Rotate Secrets**: Any secret that was in history is **COMPROMISED**. Rotate it immediately.
