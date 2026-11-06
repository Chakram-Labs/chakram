package main

import (
	"flag"
	"fmt"
	"os"
)

func cmdKeys(args []string) {
	fs := flag.NewFlagSet("keys", flag.ExitOnError)
	_ = fs.Parse(args)

	sub := "list"
	if len(fs.Args()) > 0 {
		sub = fs.Args()[0]
	}

	switch sub {
	case "list":
		keysListCmd(fs.Args()[1:])
	case "export":
		keysExportCmd(fs.Args()[1:])
	case "show":
		keysShowCmd(fs.Args()[1:])
	default:
		fmt.Fprintf(os.Stderr, "chakramctl keys: unknown subcommand %q\n", sub)
		fmt.Fprintln(os.Stderr, "Available: list, show, export")
		os.Exit(1)
	}
}

func keysListCmd(args []string) {
	fs := flag.NewFlagSet("keys list", flag.ExitOnError)
	projectDir := projectDirFlag(fs)
	_ = fs.Parse(args)

	// Show validator key via docker exec.
	out, err := composeOutput(*projectDir, "exec", "-T", "chakram",
		"cat", "/home/chakram/.chakram/config/priv_validator_key.json")
	if err != nil {
		// If exec fails (container not running), look for mounted key files.
		fmt.Println("Container not running. Mount key files to inspect them.")
		return
	}
	fmt.Println("Validator key (priv_validator_key.json):")
	fmt.Println(out)
}

func keysShowCmd(args []string) {
	fs := flag.NewFlagSet("keys show", flag.ExitOnError)
	projectDir := projectDirFlag(fs)
	_ = fs.Parse(args)

	out, err := composeOutput(*projectDir, "exec", "-T", "chakram",
		"sh", "-c", `cat /home/chakram/.chakram/config/node_key.json`)
	if err != nil {
		fatalf("exec failed: %v", err)
	}
	fmt.Println("Node key (node_key.json):")
	fmt.Println(out)
}

func keysExportCmd(args []string) {
	fs := flag.NewFlagSet("keys export", flag.ExitOnError)
	projectDir := projectDirFlag(fs)
	outDir := fs.String("out", ".", "output directory for exported keys")
	_ = fs.Parse(args)

	// Create output directory.
	if err := os.MkdirAll(*outDir, 0700); err != nil {
		fatalf("create output dir: %v", err)
	}

	for _, keyFile := range []string{"priv_validator_key.json", "node_key.json"} {
		out, err := composeOutput(*projectDir, "exec", "-T", "chakram",
			"cat", "/home/chakram/.chakram/config/"+keyFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not export %s: %v\n", keyFile, err)
			continue
		}
		dest := *outDir + "/" + keyFile
		if err := os.WriteFile(dest, []byte(out+"\n"), 0600); err != nil {
			fatalf("write %s: %v", dest, err)
		}
		fmt.Printf("Exported %s -> %s\n", keyFile, dest)
	}
}
