package main

import (
	"fmt"
	"os"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"

	"github.com/LarsFronius/rootlesskit/pkg/api/client"
	"github.com/LarsFronius/rootlesskit/pkg/version"
)

func main() {
	debug := false
	app := cli.NewApp()
	app.Name = "rootlessctl"
	app.Version = version.Version
	app.Usage = "RootlessKit API client"
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:        "debug",
			Usage:       "debug mode",
			Destination: &debug,
		},
		cli.StringFlag{
			Name:  "socket",
			Usage: "Path to api.sock (under the \"rootlesskit --state-dir\" directory)",
		},
	}
	app.Commands = []cli.Command{
		listPortsCommand,
		addPortsCommand,
		removePortsCommand,
	}
	app.Before = func(clicontext *cli.Context) error {
		if debug {
			logrus.SetLevel(logrus.DebugLevel)
		}
		return nil
	}
	if err := app.Run(os.Args); err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "error: %+v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
		os.Exit(1)
	}
}

func newClient(clicontext *cli.Context) (client.Client, error) {
	socketPath := clicontext.GlobalString("socket")
	if socketPath == "" {
		return nil, errors.New("please specify --socket")
	}
	return client.New(socketPath)
}
