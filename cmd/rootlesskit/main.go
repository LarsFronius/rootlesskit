package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"

	"github.com/LarsFronius/rootlesskit/pkg/child"
	"github.com/LarsFronius/rootlesskit/pkg/common"
	"github.com/LarsFronius/rootlesskit/pkg/copyup/tmpfssymlink"
	"github.com/LarsFronius/rootlesskit/pkg/network/slirp4netns"
	"github.com/LarsFronius/rootlesskit/pkg/network/vdeplugslirp"
	"github.com/LarsFronius/rootlesskit/pkg/network/vpnkit"
	"github.com/LarsFronius/rootlesskit/pkg/parent"
	"github.com/LarsFronius/rootlesskit/pkg/port/socat"
	"github.com/LarsFronius/rootlesskit/pkg/version"
)

func main() {
	pipeFDEnvKey := "_ROOTLESSKIT_PIPEFD_UNDOCUMENTED"
	iAmChild := os.Getenv(pipeFDEnvKey) != ""
	debug := false
	app := cli.NewApp()
	app.Name = "rootlesskit"
	app.Version = version.Version
	app.Usage = "the gate to the rootless world"
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:        "debug",
			Usage:       "debug mode",
			Destination: &debug,
		},
		cli.StringFlag{
			Name:  "state-dir",
			Usage: "state directory",
		},
		cli.StringFlag{
			Name:  "net",
			Usage: "network driver [host, slirp4netns, vpnkit, vdeplug_slirp]",
			Value: "host",
		},
		cli.StringFlag{
			Name:  "slirp4netns-binary",
			Usage: "path of slirp4netns binary for --net=slirp4netns",
			Value: "slirp4netns",
		},
		cli.StringFlag{
			Name:  "vpnkit-binary",
			Usage: "path of VPNKit binary for --net=vpnkit",
			Value: "vpnkit",
		},
		cli.IntFlag{
			Name:  "mtu",
			Usage: "MTU for non-host network (default: 65520 for slirp4netns, 1500 for others)",
			Value: 0, // resolved into 65520 for slirp4netns, 1500 for others
		},
		cli.StringFlag{
			Name:  "cidr",
			Usage: "CIDR for slirp4netns network (default: 10.0.2.0/24, requires slirp4netns v0.3.0+ for custom CIDR)",
		},
		cli.BoolFlag{
			Name:  "disable-host-loopback",
			Usage: "prohibit connecting to 127.0.0.1:* on the host namespace",
		},
		cli.StringSliceFlag{
			Name:  "copy-up",
			Usage: "mount a filesystem and copy-up the contents. e.g. \"--copy-up=/etc\" (typically required for non-host network)",
		},
		cli.StringFlag{
			Name:  "copy-up-mode",
			Usage: "copy-up mode [tmpfs+symlink]",
			Value: "tmpfs+symlink",
		},
		cli.StringFlag{
			Name:  "port-driver",
			Usage: "port driver for non-host network. [none, socat]",
			Value: "none",
		},
	}
	app.Before = func(context *cli.Context) error {
		if debug {
			logrus.SetLevel(logrus.DebugLevel)
		}
		return nil
	}
	app.Action = func(clicontext *cli.Context) error {
		if clicontext.NArg() < 1 {
			return errors.New("no command specified")
		}
		if iAmChild {
			childOpt, err := createChildOpt(clicontext)
			if err != nil {
				return err
			}
			return child.Child(pipeFDEnvKey, clicontext.Args(), childOpt)
		}
		parentOpt, err := createParentOpt(clicontext)
		if err != nil {
			return err
		}
		return parent.Parent(pipeFDEnvKey, parentOpt)
	}
	if err := app.Run(os.Args); err != nil {
		id := "parent"
		if iAmChild {
			id = "child " // padded to len("parent")
		}
		if debug {
			fmt.Fprintf(os.Stderr, "[rootlesskit:%s] error: %+v\n", id, err)
		} else {
			fmt.Fprintf(os.Stderr, "[rootlesskit:%s] error: %v\n", id, err)
		}
		// propagate the exit code
		code, ok := common.GetExecExitStatus(err)
		if !ok {
			code = 1
		}
		os.Exit(code)
	}
}

func parseCIDR(s string) (*net.IPNet, error) {
	if s == "" {
		return nil, nil
	}
	ip, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		return nil, err
	}
	if !ip.Equal(ipnet.IP) {
		return nil, errors.Errorf("cidr must be like 10.0.2.0/24, not like 10.0.2.100/24")
	}
	return ipnet, nil
}

func createParentOpt(clicontext *cli.Context) (*parent.Opt, error) {
	opt := &parent.Opt{}
	var err error
	opt.StateDir = clicontext.String("state-dir")
	mtu := clicontext.Int("mtu")
	if mtu < 0 || mtu > 65521 {
		// 0 is ok (stands for the driver's default)
		return nil, errors.Errorf("mtu must be <= 65521, got %d", mtu)
	}
	ipnet, err := parseCIDR(clicontext.String("cidr"))
	if err != nil {
		return nil, err
	}
	disableHostLoopback := clicontext.Bool("disable-host-loopback")
	if !disableHostLoopback {
		logrus.Warn("specifying --disable-host-loopback is highly recommended to prohibit connecting to 127.0.0.1:* on the host namespace (requires slirp4netns v0.3.0+ or VPNKit)")
	}
	switch s := clicontext.String("net"); s {
	case "host":
		// NOP
		if mtu != 0 {
			logrus.Warnf("unsupported mtu for --net=host: %d", mtu)
		}
		if ipnet != nil {
			return nil, errors.New("custom cidr is supported only for --net=slirp4netns (with slirp4netns v0.3.0+)")
		}
	case "slirp4netns":
		binary := clicontext.String("slirp4netns-binary")
		if _, err := exec.LookPath(binary); err != nil {
			return nil, err
		}
		opt.NetworkDriver = slirp4netns.NewParentDriver(binary, mtu, ipnet, disableHostLoopback)
	case "vpnkit":
		if ipnet != nil {
			return nil, errors.New("custom cidr is supported only for --net=slirp4netns (with slirp4netns v0.3.0+)")
		}
		binary := clicontext.String("vpnkit-binary")
		if _, err := exec.LookPath(binary); err != nil {
			return nil, err
		}
		opt.NetworkDriver = vpnkit.NewParentDriver(binary, mtu, disableHostLoopback)
	case "vdeplug_slirp":
		if ipnet != nil {
			return nil, errors.New("custom cidr is supported only for --net=slirp4netns (with slirp4netns v0.3.0+)")
		}
		if disableHostLoopback {
			return nil, errors.New("--disable-host-loopback is not supported for vdeplug_slirp")
		}
		opt.NetworkDriver = vdeplugslirp.NewParentDriver(mtu)
	default:
		return nil, errors.Errorf("unknown network mode: %s", s)
	}
	switch s := clicontext.String("port-driver"); s {
	case "none":
		// NOP
	case "socat":
		if opt.NetworkDriver == nil {
			return nil, errors.New("port driver requires non-host network")
		}
		if err != nil {
			return nil, err
		}
		opt.PortDriver, err = socat.New(&logrusDebugWriter{})
		if err != nil {
			return nil, err
		}
	default:
		return nil, errors.Errorf("unknown port driver: %s", s)
	}

	return opt, nil
}

type logrusDebugWriter struct {
}

func (w *logrusDebugWriter) Write(p []byte) (int, error) {
	s := strings.TrimSuffix(string(p), "\n")
	logrus.Debug(s)
	return len(p), nil
}

func createChildOpt(clicontext *cli.Context) (*child.Opt, error) {
	opt := &child.Opt{}
	switch s := clicontext.String("net"); s {
	case "host":
		// NOP
	case "slirp4netns":
		opt.NetworkDriver = slirp4netns.NewChildDriver()
	case "vpnkit":
		opt.NetworkDriver = vpnkit.NewChildDriver()
	case "vdeplug_slirp":
		opt.NetworkDriver = vdeplugslirp.NewChildDriver()
	default:
		return nil, errors.Errorf("unknown network mode: %s", s)
	}
	switch s := clicontext.String("copy-up-mode"); s {
	case "tmpfs+symlink":
		opt.CopyUpDriver = tmpfssymlink.NewChildDriver()
	default:
		return nil, errors.Errorf("unknown copy-up mode: %s", s)
	}
	opt.CopyUpDirs = clicontext.StringSlice("copy-up")
	return opt, nil
}
