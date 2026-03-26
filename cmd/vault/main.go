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
		fmt.Printf("vault v%s\n", Version)
	case "get":
		if len(os.Args) < 3 {
			fmt.Println("Usage: vault get <key>")
			os.Exit(1)
		}
		fmt.Printf("vault: secret '%s' not found\n", os.Args[2])
	case "set":
		if len(os.Args) < 4 {
			fmt.Println("Usage: vault set <key> <value>")
			os.Exit(1)
		}
		fmt.Printf("vault: stored '%s'\n", os.Args[2])
	case "list":
		fmt.Println("vault: no secrets stored")
	default:
		fmt.Printf("Unknown command: %s\n", cmd)
		cmdHelp()
	}
}

func cmdHelp() {
	fmt.Println()
	fmt.Println("  vault - Secure secrets management")
	fmt.Println()
	fmt.Println("  Usage: vault [command] [args]")
	fmt.Println()
	fmt.Println("  Commands:")
	fmt.Println("    get <key>         Get a secret")
	fmt.Println("    set <key> <val>   Store a secret")
	fmt.Println("    list              List all keys")
	fmt.Println("    delete <key>      Delete a secret")
	fmt.Println("    sync              Sync with peers")
	fmt.Println("    help              Show this help")
	fmt.Println()
}
