package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"athena/internal/email"

	"github.com/joho/godotenv"
)

func main() {
	// Load .env file
	if err := godotenv.Load(".env"); err != nil {
		log.Printf("Warning: .env file not found: %v", err)
	}

	// Create email configuration
	config := email.Config{
		SMTPHost:     os.Getenv("SMTP_HOST"),
		SMTPPort:     587, // Default to 587 for STARTTLS
		SMTPUsername: os.Getenv("SMTP_USERNAME"),
		SMTPPassword: os.Getenv("SMTP_PASSWORD"),
		FromAddress:  os.Getenv("SMTP_FROM"),
		FromName:     os.Getenv("SMTP_FROM_NAME"),
		BaseURL:      "https://athena.example.com", // Test URL
	}

	// Parse SMTP port if provided
	if portStr := os.Getenv("SMTP_PORT"); portStr != "" {
		var port int
		if _, err := fmt.Sscanf(portStr, "%d", &port); err == nil {
			config.SMTPPort = port
		}
	}

	// Validate configuration
	if config.SMTPHost == "" || config.SMTPUsername == "" || config.SMTPPassword == "" {
		log.Fatal("Missing required SMTP configuration. Please check your .env file.")
	}

	// Create email service
	emailService := email.NewService(&config)

	// Send test email
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	log.Printf("Sending test email to yegamble@gmail.com...")
	log.Printf("Using SMTP server: %s:%d", config.SMTPHost, config.SMTPPort)
	log.Printf("From: %s <%s>", config.FromName, config.FromAddress)

	// Send verification email (as a test)
	err := emailService.SendVerificationEmail(
		ctx,
		"test@athenaemail.com",
		"Test User",
		"test-verification-token-12345",
		"123456",
	)

	if err != nil {
		log.Fatalf("Failed to send email: %v", err)
	}

	log.Println("Test email sent successfully!")
}
