#!/bin/bash

# clean-git-history.sh
# Guide and helper for cleaning sensitive files (like .env) from git history.
# Referenced by docs/security/GIT_HISTORY_CLEANUP.md
# WARNING: This script uses git filter-branch/bfg which rewrites history.
# You will need to force push after this.

echo "========================================================"
echo "   WARNING: GIT HISTORY CLEANUP"
echo "========================================================"
echo "This script helps you remove sensitive files from git history."
echo "It requires the 'bfg' tool (recommended) or uses 'git filter-branch'."
echo ""
echo "PREREQUISITES:"
echo "1. Backup your repository! (cp -r . ../repo-backup)"
echo "2. Ensure all team members have pushed their changes."
echo "3. Stop all CI/CD pipelines."
echo ""

read -p "Are you sure you want to proceed? (y/N) " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "Aborted."
    exit 1
fi

FILE_TO_REMOVE=".env"

if command -v bfg &> /dev/null; then
    echo "Using BFG Repo-Cleaner..."
    bfg --delete-files "$FILE_TO_REMOVE"
else
    echo "BFG not found. Falling back to git filter-branch (slower)..."
    read -p "This is slow and complex. Proceed? (y/N) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        exit 1
    fi

    git filter-branch --force --index-filter \
    "git rm --cached --ignore-unmatch $FILE_TO_REMOVE" \
    --prune-empty --tag-name-filter cat -- --all
fi

echo ""
echo "========================================================"
echo "Cleanup complete locally."
echo "Step 2: Force push changes (DANGEROUS)"
echo "  git push origin --force --all"
echo "  git push origin --force --tags"
echo ""
echo "Step 3: Tell your team to re-clone the repository."
echo "========================================================"
