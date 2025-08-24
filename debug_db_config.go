package main

import (
	"fmt"
	"os"

	"athena/internal/testutil"

	"github.com/joho/godotenv"
)

func main() {
	// Try loading env files commonly used in tests
	// Load .env.test first (overrides), then .env if present; ignore errors silently
	_ = godotenv.Load(".env.test")
	_ = godotenv.Load()

	fmt.Println("=== Environment Variables ===")
	fmt.Printf("CI: %q\n", os.Getenv("CI"))
	fmt.Printf("GITHUB_ACTIONS: %q\n", os.Getenv("GITHUB_ACTIONS"))
	fmt.Printf("TEST_DATABASE_URL: %q\n", os.Getenv("TEST_DATABASE_URL"))
	fmt.Printf("DATABASE_URL: %q\n", os.Getenv("DATABASE_URL"))
	fmt.Printf("TEST_DB_HOST: %q\n", os.Getenv("TEST_DB_HOST"))
	fmt.Printf("TEST_DB_PORT: %q\n", os.Getenv("TEST_DB_PORT"))
	fmt.Printf("TEST_DB_NAME: %q\n", os.Getenv("TEST_DB_NAME"))
	fmt.Printf("TEST_DB_USER: %q\n", os.Getenv("TEST_DB_USER"))
	fmt.Printf("TEST_DB_PASSWORD: %q\n", os.Getenv("TEST_DB_PASSWORD"))
	fmt.Printf("TEST_DB_SSLMODE: %q\n", os.Getenv("TEST_DB_SSLMODE"))

	fmt.Println("\n=== Port Detection Logic ===")
	defaultPort := "5433"
	if os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != "" {
		defaultPort = "5432"
		fmt.Println("Detected CI environment, using port 5432")
	} else {
		fmt.Println("Local environment, using port 5433")
	}

	port := os.Getenv("TEST_DB_PORT")
	if port == "" {
		port = defaultPort
	}
	fmt.Printf("Final port: %s\n", port)

	// Print what the database URL would be
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		host := os.Getenv("TEST_DB_HOST")
		if host == "" {
			host = "localhost"
		}
		name := os.Getenv("TEST_DB_NAME")
		if name == "" {
			name = "athena_test"
		}
		user := os.Getenv("TEST_DB_USER")
		if user == "" {
			user = "test_user"
		}
		pass := os.Getenv("TEST_DB_PASSWORD")
		if pass == "" {
			pass = "test_password"
		}
		ssl := os.Getenv("TEST_DB_SSLMODE")
		if ssl == "" {
			ssl = "disable"
		}
		dbURL = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s", user, pass, host, port, name, ssl)
	}
	fmt.Printf("\nFinal DATABASE_URL: %s\n", dbURL)

	// Try to setup a test DB to see what happens
	fmt.Println("\n=== Testing Database Connection ===")
	// This would normally be called in a test context, but we'll just see what happens
	// testDB := testutil.SetupTestDB(nil) // Can't call this without *testing.T
	fmt.Println("Would attempt to connect with above URL")
}