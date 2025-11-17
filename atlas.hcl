# Atlas configuration for Athena video platform
# https://atlasgo.io/

# Development environment (local)
env "dev" {
  # Database URL for local development
  url = getenv("DATABASE_URL")

  # Development-specific database for schema diffing
  dev = "postgres://athena_user:athena_password@localhost:5433/athena_shadow?sslmode=disable"

  # Migration directory
  migration {
    dir = "file://migrations"
  }

  # Automatic diff generation
  diff {
    skip {
      # Skip destructive changes in dev (can be overridden with --force)
      drop_table   = false
      drop_column  = false
      drop_index   = false
    }
  }

  # Lint configuration for development
  lint {
    destructive {
      error = false  # Warn but don't block in dev
    }

    # Review data-dependent changes
    data_depend {
      error = false
    }

    # Check for incompatible changes
    incompatible {
      error = false
    }
  }

  # Format configuration
  format {
    migrate {
      apply = format(
        "{{ json . | json_merge %q }}",
        jsonencode({
          Indent = "  "
        })
      )
    }
  }
}

# Production environment
env "prod" {
  # Production database (from environment)
  url = getenv("DATABASE_URL")

  # Shadow database for migration validation
  dev = getenv("SHADOW_DATABASE_URL")

  # Migration directory
  migration {
    dir = "file://migrations"
  }

  # Strict safety checks for production
  diff {
    skip {
      # Never allow destructive changes without explicit review
      drop_table   = true
      drop_column  = true
      drop_index   = true
    }
  }

  # Production lint rules - strict enforcement
  lint {
    # Block destructive changes
    destructive {
      error = true
    }

    # Data-dependent changes require review
    data_depend {
      error = true
    }

    # Incompatible changes blocked
    incompatible {
      error = true
    }

    # Ensure concurrent index creation for large tables
    concurrent_index {
      error = false  # Warn but allow (some scenarios need non-concurrent)
    }
  }

  # Require approval before apply
  auto_approve = false

  # Format configuration
  format {
    migrate {
      apply = format(
        "{{ json . | json_merge %q }}",
        jsonencode({
          Indent = "  "
        })
      )
    }
  }
}

# CI environment (GitHub Actions)
env "ci" {
  # Test database
  url = getenv("DATABASE_URL")

  # Shadow database for validation
  dev = getenv("SHADOW_DATABASE_URL")

  migration {
    dir = "file://migrations"
  }

  # CI-specific checks
  lint {
    latest = 1  # Only lint new migrations (not entire history)

    # Strict checks for CI
    destructive {
      error = true
    }

    data_depend {
      error = true
    }

    incompatible {
      error = true
    }

    # Allow concurrent index creation
    concurrent_index {
      error = false
    }
  }

  # Format configuration
  format {
    migrate {
      apply = format(
        "{{ json . | json_merge %q }}",
        jsonencode({
          Indent = "  "
        })
      )
    }
  }
}

# Testing environment (for migration testing)
env "test" {
  # Test database URL
  url = getenv("TEST_DATABASE_URL")

  # Shadow database
  dev = "postgres://test_user:test_password@localhost:5433/athena_shadow?sslmode=disable"

  migration {
    dir = "file://migrations"
  }

  # Relaxed checks for testing
  diff {
    skip {
      drop_table   = false
      drop_column  = false
      drop_index   = false
    }
  }

  lint {
    destructive {
      error = false
    }
  }

  # Auto-approve in test environment
  auto_approve = true
}
