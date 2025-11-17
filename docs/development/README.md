# Development Documentation

This directory contains development guides, code quality reports, and testing documentation.

## Testing Documentation

- **[TEST_EXECUTION_GUIDE.md](TEST_EXECUTION_GUIDE.md)** - Comprehensive testing guide
- **[TEST_BASELINE_REPORT.md](TEST_BASELINE_REPORT.md)** - Test coverage baseline and metrics
- **[VIRUS_SCANNER_TEST_REPORT.md](VIRUS_SCANNER_TEST_REPORT.md)** - Virus scanner test report
- **[VIRUS_SCANNER_TEST_SUMMARY.md](VIRUS_SCANNER_TEST_SUMMARY.md)** - Virus scanner test summary
- **[QUICK_REFERENCE_VIRUS_SCANNER_TESTS.md](QUICK_REFERENCE_VIRUS_SCANNER_TESTS.md)** - Quick reference for virus scanner tests

## Code Quality

- **[CODE_QUALITY_REVIEW.md](CODE_QUALITY_REVIEW.md)** - Code quality assessment and recommendations
- **[LINT_FIXES_SUMMARY.md](LINT_FIXES_SUMMARY.md)** - Summary of linting fixes and improvements
- **[GOROUTINE_LEAK_FIX.md](GOROUTINE_LEAK_FIX.md)** - Goroutine leak detection and fixes

## Refactoring & Improvements

- **[REFACTORING_STATUS.md](REFACTORING_STATUS.md)** - Current refactoring status
- **[REFACTORING_FIXES_SUMMARY.md](REFACTORING_FIXES_SUMMARY.md)** - Summary of refactoring fixes
- **[IMPROVEMENTS.md](IMPROVEMENTS.md)** - Planned improvements and enhancements
- **[QUICK_WINS.md](QUICK_WINS.md)** - Quick win opportunities

## Migration & Integration

- **[MIGRATION_TO_GOOSE.md](../MIGRATION_TO_GOOSE.md)** - Atlas to Goose migration guide
- **[LOCAL_WHISPER_MIGRATION_PROGRESS.md](LOCAL_WHISPER_MIGRATION_PROGRESS.md)** - Whisper integration progress

## Quick Links

- [Main README](../../README.md)
- [Architecture Documentation](../architecture/)
- [API Examples](../API_EXAMPLES.md)
- [Sprint Documentation](../project-management/sprints/)

## Development Workflow

1. **Install pre-commit hooks**: `make install-hooks`
2. **Run linters**: `make lint`
3. **Run tests**: `make test`
4. **Check coverage**: `make test-coverage`
5. **Run migrations**: `make migrate-up`

## Test Coverage Metrics

- **Total Test Files**: 156
- **Code Coverage**: 85%+
- **Security Tests**: 50+
- **Integration Tests**: Comprehensive
