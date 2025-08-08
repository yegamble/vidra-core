package main

import (
	"fmt"
	"log"
	"os"

	"github.com/yourname/gotube/internal/config"
	"github.com/yourname/gotube/internal/ipfs"
)

func main() {
	// Set the IPFS endpoint to test Docker connection
	os.Setenv("IPFS_PATH", "http://localhost:5001")

	cfg := config.Load()
	fmt.Printf("Using IPFS endpoint: %s\n", cfg.IPFSPath)

	client, err := ipfs.NewClient(cfg)
	if err != nil {
		log.Fatalf("Failed to create IPFS client: %v", err)
	}

	// Test the connection by getting node ID
	id, err := client.ID()
	if err != nil {
		log.Fatalf("Failed to get IPFS node ID: %v", err)
	}

	fmt.Printf("Successfully connected to IPFS!\n")
	fmt.Printf("IPFS Node ID: %s\n", id.ID)
	fmt.Printf("IPFS Agent Version: %s\n", id.AgentVersion)
	fmt.Println("✅ Connection between Golang API and IPFS is working!")
}
