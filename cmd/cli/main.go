package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"athena/internal/backup"
	"athena/internal/config"
	"athena/internal/database"

	"github.com/jmoiron/sqlx"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

const version = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "backup":
		handleBackup(os.Args[2:])
	case "restore":
		handleRestore(os.Args[2:])
	case "status":
		handleStatus(os.Args[2:])
	case "migrate":
		handleMigrate(os.Args[2:])
	case "setup":
		handleSetup(os.Args[2:])
	case "version":
		fmt.Printf("athena-cli version %s\n", version)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("athena-cli - Athena command-line tool")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  athena-cli <command> [options]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  backup     Manage backups")
	fmt.Println("  restore    Restore from backup")
	fmt.Println("  status     Show system status")
	fmt.Println("  migrate    Manage database migrations")
	fmt.Println("  setup      Interactive setup")
	fmt.Println("  version    Show version")
	fmt.Println("  help       Show this help")
	fmt.Println()
	fmt.Println("Run 'athena-cli <command> -h' for command-specific help")
}

func handleBackup(args []string) {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	jsonOutput := fs.Bool("json", false, "Output in JSON format")

	fs.Usage = func() {
		fmt.Println("Usage: athena-cli backup <subcommand>")
		fmt.Println()
		fmt.Println("Subcommands:")
		fmt.Println("  create    Create a new backup")
		fmt.Println("  list      List available backups")
		fmt.Println()
		fmt.Println("Options:")
		fmt.Println("  --json    Output in JSON format")
	}

	if len(args) == 0 {
		fs.Usage()
		os.Exit(1)
	}

	subcommand := args[0]
	fs.Parse(args[1:])

	cfg := loadConfig()
	ctx := context.Background()

	switch subcommand {
	case "create":
		createBackup(ctx, cfg, *jsonOutput)
	case "list":
		listBackups(ctx, cfg, *jsonOutput)
	default:
		fmt.Fprintf(os.Stderr, "Unknown backup subcommand: %s\n", subcommand)
		fs.Usage()
		os.Exit(1)
	}
}

func createBackup(ctx context.Context, cfg *config.Config, jsonOutput bool) {
	backupPath := os.Getenv("BACKUP_LOCAL_PATH")
	if backupPath == "" {
		backupPath = "./backups"
	}
	target := backup.NewLocalBackend(backupPath)

	db, err := sqlx.Connect("postgres", cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	version, _ := database.CurrentVersion(db)
	manager := backup.NewBackupManager(target, "cli", version, cfg.DatabaseURL, cfg.RedisURL)

	result, err := manager.CreateBackup(ctx)
	if err != nil {
		log.Fatalf("Backup failed: %v", err)
	}

	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(result)
	} else {
		fmt.Printf("Backup completed successfully\n")
		fmt.Printf("  Path: %s\n", result.BackupPath)
		fmt.Printf("  Size: %d bytes\n", result.BytesSize)
		fmt.Printf("  Schema: v%d\n", result.SchemaVersion)
	}
}

func listBackups(ctx context.Context, cfg *config.Config, jsonOutput bool) {
	backupPath := os.Getenv("BACKUP_LOCAL_PATH")
	if backupPath == "" {
		backupPath = "./backups"
	}
	target := backup.NewLocalBackend(backupPath)

	backups, err := target.List(ctx, "")
	if err != nil {
		log.Fatalf("Failed to list backups: %v", err)
	}

	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(backups)
	} else {
		fmt.Printf("Found %d backup(s):\n\n", len(backups))
		for _, b := range backups {
			fmt.Printf("  %s\n", b.Path)
			fmt.Printf("    Size: %d bytes\n", b.Size)
			fmt.Printf("    Date: %s\n", b.ModTime.Format(time.RFC3339))
			fmt.Println()
		}
	}
}

func handleRestore(args []string) {
	fmt.Println("Restore functionality not yet fully implemented")
}

func handleStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	jsonOutput := fs.Bool("json", false, "Output in JSON format")
	fs.Parse(args)

	cfg := loadConfig()

	db, err := sqlx.Connect("postgres", cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Database connection failed: %v", err)
	}
	defer db.Close()

	version, _ := database.CurrentVersion(db)

	if *jsonOutput {
		status := map[string]interface{}{
			"database_connected": true,
			"schema_version":     version,
		}
		json.NewEncoder(os.Stdout).Encode(status)
	} else {
		fmt.Println("Athena Status:")
		fmt.Printf("  Database: Connected\n")
		fmt.Printf("  Schema Version: v%d\n", version)
	}
}

func handleMigrate(args []string) {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	fs.Parse(args)

	cfg := loadConfig()

	db, err := sqlx.Connect("postgres", cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Database connection failed: %v", err)
	}
	defer db.Close()

	fmt.Println("Running database migrations...")
	if err := database.RunMigrations(context.Background(), db); err != nil {
		log.Fatalf("Migration failed: %v", err)
	}

	fmt.Println("Migrations completed successfully")
}

func handleSetup(args []string) {
	fmt.Println("Interactive setup not yet fully implemented")
	fmt.Println("Use the web-based setup wizard at http://localhost:8080/setup")
}

func loadConfig() *config.Config {
	if err := godotenv.Load(); err != nil {
		log.Println("Warning: .env file not found, using environment variables")
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	return cfg
}
