package main

import (
	"fmt"
	"os"
)

const Version = "1.0.0"

func main() {
	if len(os.Args) < 2 {
		cmdHelp()
		return
	}

	cmd := os.Args[1]

	switch cmd {
	case "help", "-h", "--help":
		cmdHelp()
	case "version", "-v", "--version":
		fmt.Printf("meshdb v%s\n", Version)
	case "start":
		fmt.Println("meshdb: local database service")
		fmt.Println("  (Not yet implemented)")
	case "status":
		fmt.Println("meshdb: not running")
	default:
		fmt.Printf("Unknown command: %s\n", cmd)
		cmdHelp()
	}
}

func cmdHelp() {
	fmt.Println()
	fmt.Println("  meshdb - Local distributed database")
	fmt.Println()
	fmt.Println("  Usage: meshdb [command]")
	fmt.Println()
	fmt.Println("  Commands:")
	fmt.Println("    start       Start meshdb service")
	fmt.Println("    stop        Stop meshdb service")
	fmt.Println("    status      Show service status")
	fmt.Println("    query       Execute a query")
	fmt.Println("    sync        Sync with peers")
	fmt.Println("    help        Show this help")
	fmt.Println()
}
