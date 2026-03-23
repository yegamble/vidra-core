# Git History Cleanup Guide

**WARNING: DESTRUCTIVE OPERATION**

This guide explains how to remove sensitive files (like `.env`) from the git history.
This process involves rewriting the git history and requires a forced push, which will affect all collaborators.

## Prerequisites

1. **Backup the repository:** Make a local copy of the entire repository directory before starting.

    ```bash
    cp -r athena athena-backup
    ```

2. **Notify the team:** Inform all developers to stop pushing changes. They will need to re-clone the repository after this operation.
3. **Stop CI/CD:** Ensure no automated pipelines are running.

## Method 1: Using the Cleanup Script (Recommended)

We have provided a helper script that uses `bfg` (if installed) or `git filter-branch` to remove the `.env` file.

1. **Run the script:**

    ```bash
    ./scripts/clean-git-history.sh
    ```

2. **Follow the prompts:** The script will ask for confirmation.
3. **Force Push:**
    Once the script completes locally, you must push the changes to the remote repository.

    ```bash
    git push origin --force --all
    git push origin --force --tags
    ```

## Method 2: Manual Cleanup (Fallback)

If the script fails or you prefer to run the commands manually, follow these steps.

### Option A: Using BFG Repo-Cleaner (Fastest)

1. Download and install [BFG Repo-Cleaner](https://rtyley.github.io/bfg-repo-cleaner/).
2. Run BFG to delete the file:

    ```bash
    bfg --delete-files .env
    ```

3. Clean the reflog and garbage collect:

    ```bash
    git reflog expire --expire=now --all && git gc --prune=now --aggressive
    ```

4. Force push:

    ```bash
    git push origin --force --all
    git push origin --force --tags
    ```

### Option B: Using git filter-branch (Native)

1. Run the filter-branch command:

    ```bash
    git filter-branch --force --index-filter \
    "git rm --cached --ignore-unmatch .env" \
    --prune-empty --tag-name-filter cat -- --all
    ```

2. Clean the reflog and garbage collect:

    ```bash
    rm -rf .git/refs/original/
    git reflog expire --expire=now --all
    git gc --prune=now --aggressive
    ```

3. Force push:

    ```bash
    git push origin --force --all
    git push origin --force --tags
    ```

## Post-Cleanup

After the force push:

1. **Verify** that the sensitive file is no longer in the history (e.g., check GitHub history for a commit that used to contain it).
2. **Instruct the team** to delete their local repositories and clone a fresh copy.

    ```bash
    # Developer machine
    cd ..
    rm -rf athena
    git clone https://github.com/yegamble/athena.git
    ```
