# pkg - Shared Utility Packages

This directory contains reusable utility packages that have **no dependencies** on `internal/` packages. These packages follow Go's convention for public, reusable libraries.

## Packages

### `strutil` - String Utilities

Provides common string manipulation and conversion functions:

- SQL null string conversions (`NullStringToPtr`, `PtrToNullString`)
- String pointer helpers (`StringPtr`, `StringValue`)
- Text processing (`TruncateWithEllipsis`, `NormalizeWhitespace`)
- Slice operations (`Contains`, `Filter`, `Map`, `TrimNonEmpty`)

**Example**:

```go
import "vidra-core/pkg/strutil"

// Convert SQL null string
ptr := strutil.NullStringToPtr(sqlNullString)

// Create string pointer inline
name := strutil.StringPtr("example")

// Truncate with ellipsis
summary := strutil.TruncateWithEllipsis(longText, 100)
```

## Adding New Packages

When adding new packages to `pkg/`:

1. **No Internal Dependencies**: Packages must not import `vidra/internal/*`
2. **Well-Tested**: Include comprehensive unit tests
3. **Documented**: Add godoc comments for all exported functions
4. **Focused**: Each package should have a single, clear purpose

## vs. internal/

- **`pkg/`**: Reusable utilities with no internal dependencies (can be extracted to separate modules)
- **`internal/`**: Application-specific code with internal dependencies (cannot be imported by external projects)
