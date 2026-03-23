# Virus Scanner Test Fixtures

This directory contains test files for virus scanning and file type blocking tests.

## Clean Files

### clean_file.txt
- **Purpose**: Test that clean text files pass virus scanning
- **Size**: < 1KB
- **Expected**: CLEAN

### clean_video.mp4
- **Purpose**: Test that legitimate video files pass scanning
- **Size**: < 1KB (minimal MP4 header)
- **Expected**: CLEAN

### large_clean.bin
- **Purpose**: Performance testing for large file scanning
- **Size**: 100MB
- **Expected**: CLEAN
- **Note**: Tests streaming scan capability and memory usage

## Malware Test Files

### eicar.txt
- **Purpose**: EICAR test virus for antivirus testing
- **Content**: Standard EICAR test string
- **Expected**: INFECTED (virus name contains "EICAR")
- **Note**: This is NOT real malware - it's a standard test file

**About EICAR**: The EICAR test file is a legitimate test file developed by the
European Institute for Computer Antivirus Research and EICAR. It's designed to
test antivirus software without using actual malware. All antivirus software
detects it as a test virus.

## Blocked File Types

The `blocked_types/` directory contains examples of file types that should be
blocked according to CLAUDE.md security requirements:

- **test.exe** - Windows executable (MZ header)
- **test.bat** - Batch script
- **test.ps1** - PowerShell script
- **test.sh** - Shell script
- **test.py** - Python script

These files should be rejected regardless of content.

## Archive Test Files

### nested.zip
- **Purpose**: Test ZIP nesting depth limits
- **Contents**: ZIP containing ZIP containing ZIP... (5+ levels)
- **Expected**: BLOCKED (exceeds max nesting depth)

## Usage

These fixtures are used by:
- `internal/security/virus_scanner_test.go`
- `internal/security/file_type_blocker_test.go`

Run tests with:
```bash
go test ./internal/security/...
```

Or with Docker:
```bash
docker compose --profile test up clamav-test
```

## Security Note

The EICAR file is the ONLY file in this directory that will be detected as
malware. It is a harmless test file. Do NOT add actual malware samples to
this directory.

If you need to test real malware detection, use the EICAR file or consult
with your security team for approved test samples.
