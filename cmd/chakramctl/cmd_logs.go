package main

import (
	"flag"
	"fmt"
)

func cmdLogs(args []string) {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	projectDir := projectDirFlag(fs)
	service := fs.String("service", "", "specific service name (default: all)")
	follow := fs.Bool("f", true, "follow log output")
	tail := fs.String("tail", "100", "number of lines to show from end")
	_ = fs.Parse(args)

	if err := ensureDockerRunning(); err != nil {
		fatalf("%v", err)
	}

	logArgs := []string{"logs", "--tail", *tail}
	if *follow {
		logArgs = append(logArgs, "-f")
	}
	if *service != "" {
		logArgs = append(logArgs, *service)
	}

	fmt.Printf("Following logs (Ctrl+C to stop)...\n\n")
	_ = composeRun(*projectDir, logArgs...)
}
