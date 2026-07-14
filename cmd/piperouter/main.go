// Command piperouter is the PipeRouter binary (PRD §21.3):
//
//	piperouter [serve] [flags]   run the proxy (default command)
//	piperouter validate [flags]  check a configuration file and exit
//	piperouter version           print version information
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/kites262/piperouter/internal/app"
	"github.com/kites262/piperouter/internal/config"
)

// version is stamped at build time via
// `-ldflags "-X main.version=..."` (see Makefile).
var version = "0.3.0-dev"

const defaultConfigPath = "piperouter.yaml"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches the subcommand and returns the process exit code. It is
// separated from main so the CLI surface is unit-testable (§22.6).
func run(args []string, stdout, stderr io.Writer) int {
	cmd := "serve"
	rest := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		rest = args[1:]
	}

	switch cmd {
	case "serve":
		opts, code := parseServeOptions(rest, stderr)
		if code >= 0 {
			return code
		}
		if err := app.Run(context.Background(), opts); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		return 0
	case "validate":
		return runValidate(rest, stdout, stderr)
	case "version":
		printVersion(stdout)
		return 0
	case "help", "--help", "-h":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", cmd)
		usage(stderr)
		return 2
	}
}

// parseServeOptions parses the serve flag set. It returns code == -1 on
// success; any code >= 0 means "exit now with this code" (flag error or -h).
func parseServeOptions(args []string, stderr io.Writer) (app.Options, int) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var opts app.Options
	fs.StringVar(&opts.ConfigPath, "config", defaultConfigPath, "path to the configuration file")
	fs.StringVar(&opts.ProxyListen, "proxy-listen", "", "override server.proxy.listen (runtime only, not persisted)")
	fs.StringVar(&opts.AdminListen, "admin-listen", "", "override server.admin.listen (runtime only, not persisted)")
	fs.BoolVar(&opts.DisableAdmin, "disable-admin", false, "disable the admin API and WebUI")
	fs.BoolVar(&opts.DisableWeb, "disable-web", false, "disable the WebUI (admin API stays on)")
	fs.StringVar(&opts.LogLevel, "log-level", "", "override runtime.log_level (debug|info|warn|error)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return app.Options{}, 0
		}
		return app.Options{}, 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "serve: unexpected argument %q\n", fs.Arg(0))
		return app.Options{}, 2
	}
	opts.Version = version
	return opts, -1
}

// runValidate loads and validates a configuration file, printing every
// problem one per line to stderr (PRD §21.3).
func runValidate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", defaultConfigPath, "path to the configuration file")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "validate: unexpected argument %q\n", fs.Arg(0))
		return 2
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := config.Validate(cfg, config.ConfigBaseDir(*cfgPath)); err != nil {
		var verr *config.ValidationError
		if errors.As(err, &verr) {
			for _, issue := range verr.Issues {
				fmt.Fprintln(stderr, issue)
			}
		} else {
			fmt.Fprintln(stderr, err)
		}
		return 1
	}
	fmt.Fprintln(stdout, "configuration valid")
	return 0
}

func printVersion(w io.Writer) {
	fmt.Fprintf(w, "piperouter %s (%s)\n", version, runtime.Version())
}

func usage(w io.Writer) {
	fmt.Fprintf(w, `piperouter — single-binary HTTP distribution proxy

Usage:
  piperouter [serve] [flags]    run the proxy (default command)
  piperouter validate [flags]   validate a configuration file
  piperouter version            print version information

Flags (serve):
  --config string         path to the configuration file (default %q)
  --proxy-listen string   override server.proxy.listen
  --admin-listen string   override server.admin.listen
  --disable-admin         disable the admin API and WebUI
  --disable-web           disable the WebUI
  --log-level string      override runtime.log_level (debug|info|warn|error)

Flags (validate):
  --config string         path to the configuration file (default %q)

CLI flags take precedence over the configuration file and are never
written back to it.
`, defaultConfigPath, defaultConfigPath)
}
