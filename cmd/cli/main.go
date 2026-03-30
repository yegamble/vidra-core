package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"vidra-core/internal/backup"
	"vidra-core/internal/config"
	"vidra-core/internal/database"

	"github.com/jmoiron/sqlx"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

const version = "dev"

const defaultBackupPath = "./backups"

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
		fmt.Printf("vidra-cli version %s\n", version)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("vidra-cli - Vidra Core command-line tool")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  vidra-cli <command> [options]")
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
	fmt.Println("Run 'vidra-cli <command> -h' for command-specific help")
}

func handleBackup(args []string) {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	jsonOutput := fs.Bool("json", false, "Output in JSON format")
	includeDB := fs.Bool("include-db", true, "Include database in backup")
	includeRedis := fs.Bool("include-redis", true, "Include Redis in backup")
	includeStorage := fs.Bool("include-storage", true, "Include storage in backup")
	excludeDirs := fs.String("exclude-dir", "", "Comma-separated list of directories to exclude from storage backup")

	fs.Usage = func() {
		fmt.Println("Usage: vidra-cli backup <subcommand> [options]")
		fmt.Println()
		fmt.Println("Subcommands:")
		fmt.Println("  create    Create a new backup")
		fmt.Println("  list      List available backups")
		fmt.Println()
		fmt.Println("Options:")
		fmt.Println("  --json                 Output in JSON format")
		fmt.Println("  --include-db=true      Include database in backup (default: true)")
		fmt.Println("  --include-redis=true   Include Redis in backup (default: true)")
		fmt.Println("  --include-storage=true Include storage in backup (default: true)")
		fmt.Println("  --exclude-dir=DIRS     Comma-separated directories to exclude (e.g., 'videos,thumbnails')")
	}

	if len(args) == 0 {
		fs.Usage()
		os.Exit(1)
	}

	subcommand := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		log.Fatalf("Failed to parse flags: %v", err)
	}

	cfg := loadConfig()
	ctx := context.Background()

	switch subcommand {
	case "create":
		components := backup.BackupComponents{
			IncludeDatabase: *includeDB,
			IncludeRedis:    *includeRedis,
			IncludeStorage:  *includeStorage,
		}
		if *excludeDirs != "" {
			components.ExcludeDirs = strings.Split(*excludeDirs, ",")
			for i := range components.ExcludeDirs {
				components.ExcludeDirs[i] = strings.TrimSpace(components.ExcludeDirs[i])
			}
		}
		createBackup(ctx, cfg, components, *jsonOutput)
	case "list":
		listBackups(ctx, cfg, *jsonOutput)
	default:
		fmt.Fprintf(os.Stderr, "Unknown backup subcommand: %s\n", subcommand)
		fs.Usage()
		os.Exit(1)
	}
}

func createBackup(ctx context.Context, cfg *config.Config, components backup.BackupComponents, jsonOutput bool) {
	target := newLocalBackend()

	db := mustConnectDB(cfg.DatabaseURL)
	defer db.Close()

	version, _ := database.CurrentVersion(db)
	manager := backup.NewBackupManager(target, "cli", version, cfg.DatabaseURL, cfg.RedisURL, cfg.StorageDir)
	manager.Components = components

	result, err := manager.CreateBackup(ctx)
	if err != nil {
		log.Fatalf("Backup failed: %v", err)
	}

	if jsonOutput {
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			log.Fatalf("Failed to encode JSON: %v", err)
		}
	} else {
		fmt.Printf("Backup completed successfully\n")
		fmt.Printf("  Path: %s\n", result.BackupPath)
		fmt.Printf("  Size: %d bytes\n", result.BytesSize)
		fmt.Printf("  Schema: v%d\n", result.SchemaVersion)
		fmt.Printf("  Components: %v\n", components.GetIncludedComponents())
	}
}

func listBackups(ctx context.Context, _ *config.Config, jsonOutput bool) {
	target := newLocalBackend()

	backups, err := target.List(ctx, "")
	if err != nil {
		log.Fatalf("Failed to list backups: %v", err)
	}

	if jsonOutput {
		if err := json.NewEncoder(os.Stdout).Encode(backups); err != nil {
			log.Fatalf("Failed to encode JSON: %v", err)
		}
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
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	backupID := fs.String("backup", "", "Backup ID or path to restore")
	latest := fs.Bool("latest", false, "Restore from most recent backup")
	noPreBackup := fs.Bool("no-pre-backup", false, "Skip pre-restore backup (dangerous)")
	jsonOutput := fs.Bool("json", false, "Output in JSON format")

	fs.Usage = func() {
		fmt.Println("Usage: vidra-cli restore [options]")
		fmt.Println()
		fmt.Println("Options:")
		fmt.Println("  --backup <id>       Backup ID or path to restore")
		fmt.Println("  --latest            Restore from most recent backup")
		fmt.Println("  --no-pre-backup     Skip pre-restore backup (dangerous)")
		fmt.Println("  --json              Output in JSON format")
	}

	if err := fs.Parse(args); err != nil {
		log.Fatalf("Failed to parse flags: %v", err)
	}

	if *backupID == "" && !*latest {
		fmt.Fprintln(os.Stderr, "Error: either --backup or --latest must be specified")
		fs.Usage()
		os.Exit(1)
	}

	cfg := loadConfig()
	ctx := context.Background()

	target := newLocalBackend()
	*backupID = resolveRestoreBackupPath(ctx, target, *backupID, *latest)

	db := mustConnectDB(cfg.DatabaseURL)
	defer db.Close()

	version, _ := database.CurrentVersion(db)
	backupManager := backup.NewBackupManager(target, "cli", version, cfg.DatabaseURL, cfg.RedisURL, cfg.StorageDir)

	tempDir, err := os.MkdirTemp("", "vidra-restore-*")
	if err != nil {
		log.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	restoreManager := backup.NewRestoreManager(target, tempDir)
	restoreManager.DatabaseURL = cfg.DatabaseURL
	restoreManager.BackupMgr = backupManager
	restoreManager.CurrentSchema = version

	if !*jsonOutput {
		fmt.Printf("Restoring from backup: %s\n", *backupID)
	}

	progressChan := make(chan backup.RestoreProgress, 10)
	errChan := make(chan error, 1)

	go func() {
		errChan <- restoreManager.Restore(ctx, backup.RestoreOptions{
			BackupPath:      *backupID,
			CreatePreBackup: !*noPreBackup,
			RunMigrations:   true,
		}, progressChan)
	}()

	for progress := range progressChan {
		if !*jsonOutput {
			if progress.Error != "" {
				fmt.Fprintf(os.Stderr, "Error in %s: %s\n", progress.Stage, progress.Error)
			} else {
				fmt.Printf("[%s] %.0f%% - %s\n", progress.Stage, progress.Progress*100, progress.Message)
			}
		}
	}

	err = <-errChan

	if err != nil {
		if *jsonOutput {
			if encErr := json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
				"success": false,
				"error":   err.Error(),
			}); encErr != nil {
				log.Fatalf("Failed to encode JSON: %v", encErr)
			}
		} else {
			log.Fatalf("Restore failed: %v", err)
		}
		os.Exit(1)
	}

	if *jsonOutput {
		if err := json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
			"success": true,
			"message": "Restore completed successfully",
		}); err != nil {
			log.Fatalf("Failed to encode JSON: %v", err)
		}
	} else {
		fmt.Println("Restore completed successfully")
		fmt.Println("Please restart the application for changes to take effect")
	}
}

func handleStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	jsonOutput := fs.Bool("json", false, "Output in JSON format")
	if err := fs.Parse(args); err != nil {
		log.Fatalf("Failed to parse flags: %v", err)
	}

	cfg := loadConfig()

	db := mustConnectDB(cfg.DatabaseURL)
	defer db.Close()

	version, _ := database.CurrentVersion(db)

	if *jsonOutput {
		status := map[string]interface{}{
			"database_connected": true,
			"schema_version":     version,
		}
		if err := json.NewEncoder(os.Stdout).Encode(status); err != nil {
			log.Fatalf("Failed to encode JSON: %v", err)
		}
	} else {
		fmt.Println("Vidra Core Status:")
		fmt.Printf("  Database: Connected\n")
		fmt.Printf("  Schema Version: v%d\n", version)
	}
}

func handleMigrate(args []string) {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		log.Fatalf("Failed to parse flags: %v", err)
	}

	cfg := loadConfig()

	db := mustConnectDB(cfg.DatabaseURL)
	defer db.Close()

	fmt.Println("Running database migrations...")
	if err := database.RunMigrations(context.Background(), db); err != nil {
		log.Fatalf("Migration failed: %v", err)
	}

	fmt.Println("Migrations completed successfully")
}

func handleSetup(args []string) {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	fromEnv := fs.String("from-env", "", "Non-interactive setup from env template file")

	fs.Usage = func() {
		fmt.Println("Usage: vidra-cli setup [options]")
		fmt.Println()
		fmt.Println("Options:")
		fmt.Println("  --from-env <file>   Non-interactive setup from env template")
		fmt.Println()
		fmt.Println("Without --from-env, runs interactive setup wizard")
	}

	if err := fs.Parse(args); err != nil {
		log.Fatalf("Failed to parse flags: %v", err)
	}

	if *fromEnv != "" {
		fmt.Printf("Loading configuration from %s...\n", *fromEnv)
		if err := godotenv.Load(*fromEnv); err != nil {
			log.Fatalf("Failed to load env file: %v", err)
		}
		fmt.Println("Configuration loaded successfully")
		fmt.Println("Start the server with: go run ./cmd/server")
		return
	}

	fmt.Println("Vidra Core Interactive Setup")
	fmt.Println("========================")
	fmt.Println()
	fmt.Println("This CLI setup is minimal. For a full-featured setup wizard,")
	fmt.Println("use the web interface at: http://localhost:8080/setup")
	fmt.Println()
	fmt.Println("Basic configuration:")
	fmt.Println("1. Copy .env.example to .env")
	fmt.Println("2. Edit .env with your settings")
	fmt.Println("3. Run: vidra-cli migrate")
	fmt.Println("4. Start the server")
	fmt.Println()
	fmt.Println("Or use: vidra-cli setup --from-env .env.example")
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

// newLocalBackend returns a LocalBackend using BACKUP_LOCAL_PATH or defaultBackupPath.
func newLocalBackend() *backup.LocalBackend {
	backupPath := os.Getenv("BACKUP_LOCAL_PATH")
	if backupPath == "" {
		backupPath = defaultBackupPath
	}
	return backup.NewLocalBackend(backupPath)
}

// mustConnectDB connects to PostgreSQL using the provided URL or fatals.
func mustConnectDB(databaseURL string) *sqlx.DB {
	db, err := sqlx.Connect("postgres", databaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	return db
}

// resolveRestoreBackupPath returns the concrete backup path to restore from.
// If useLatest is true it lists the backend and returns the most recent entry.
func resolveRestoreBackupPath(ctx context.Context, target *backup.LocalBackend, backupID string, useLatest bool) string {
	if !useLatest {
		return backupID
	}
	backups, err := target.List(ctx, "")
	if err != nil {
		log.Fatalf("Failed to list backups: %v", err)
	}
	if len(backups) == 0 {
		log.Fatal("No backups found")
	}
	return backups[0].Path
}
