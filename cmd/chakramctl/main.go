// chakramctl is the Chakram node operator CLI.
// It drives docker compose to manage node deployments and provides
// helper commands for key management, backup/restore, and status.
//
// Usage:
//
//	chakramctl <command> [flags]
//
// Commands:
//
//	init       Initialize a new node environment
//	up         Start node services
//	down       Stop node services
//	status     Show running node status
//	logs       Follow container logs
//	keys       Key management helpers
//	reset      Wipe chain data (preserves keys)
//	backup     Backup chain data to a tar archive
//	restore    Restore chain data from a tar archive
//	version    Print version information
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "init":
		cmdInit(os.Args[2:])
	case "up":
		cmdUp(os.Args[2:])
	case "down":
		cmdDown(os.Args[2:])
	case "status":
		cmdStatus(os.Args[2:])
	case "logs":
		cmdLogs(os.Args[2:])
	case "keys":
		cmdKeys(os.Args[2:])
	case "reset":
		cmdReset(os.Args[2:])
	case "backup":
		cmdBackup(os.Args[2:])
	case "restore":
		cmdRestore(os.Args[2:])
	case "version", "--version", "-v":
		cmdVersion()
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "chakramctl: unknown command %q\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`chakramctl — Chakram node operator CLI

Usage:
  chakramctl <command> [flags]

Commands:
  init      Initialize a new node environment (generates keys, config, genesis)
  up        Start node services via docker compose
  down      Stop node services
  status    Show running node status (height, peers, sync state)
  logs      Follow container logs
  keys      Key management helpers (list, export, import)
  reset     Wipe chain data while preserving keys
  backup    Backup chain data to a tar archive
  restore   Restore chain data from a tar archive
  version   Print version information

Global flags:
  --project-dir <dir>   Directory containing docker-compose.yml (default: current directory)

Run 'chakramctl <command> --help' for more information on a command.
`)
}

// projectDirFlag adds --project-dir to a FlagSet and returns the pointer.
func projectDirFlag(fs *flag.FlagSet) *string {
	cwd, _ := os.Getwd()
	return fs.String("project-dir", cwd, "directory containing docker-compose.yml")
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "chakramctl: "+format+"\n", args...)
	os.Exit(1)
}
