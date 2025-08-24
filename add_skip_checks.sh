#!/bin/bash

# Add testing.Short() skip checks to upload service test functions
file="/Users/yosefgamble/github/athena/internal/usecase/upload_service_test.go"

# List of functions to update (excluding the one already done)
functions=(
    "TestUploadService_InitiateUpload_FileTooLarge"
    "TestUploadService_UploadChunk"
    "TestUploadService_UploadChunk_InvalidChecksum"
    "TestUploadService_UploadChunk_Resumable"
    "TestUploadService_CompleteUpload"
    "TestUploadService_CompleteUpload_UsesMetadataHeight"
    "TestUploadService_CompleteUpload_UsesWidthAspectRatio"
    "TestUploadService_CompleteUpload_IncompleteChunks"
    "TestUploadService_ExpiredSession"
    "TestUploadService_CleanupTempFiles"
)

# Create a backup
cp "$file" "$file.bak"

# For each function, add the skip check
for func in "${functions[@]}"; do
    sed -i '' "/func $func(t \*testing.T) {/a\\
\\	if testing.Short() {\\
\\		t.Skip(\"Skipping database tests in short mode\")\\
\\	}\\
\\
" "$file"
done

echo "Added skip checks to upload service tests"