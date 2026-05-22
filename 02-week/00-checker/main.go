package main

import (
	"fmt"
	"log"
	"os"
	"strings"
)

const namedNamespaceDir = "/run/netns"

func main() {
	args := os.Args[1:]

	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		if err := runNetNSCheck(args); err != nil {
			log.Fatal(err)
		}
		return
	}

	switch args[0] {
	case "netns":
		if err := runNetNSCheck(args[1:]); err != nil {
			log.Fatal(err)
		}
	case "veth":
		if err := runVethCheck(args[1:]); err != nil {
			log.Fatal(err)
		}
	case "server":
		if err := runServerCheck(args[1:]); err != nil {
			log.Fatal(err)
		}
	default:
		printUsage(os.Stderr)
		log.Fatalf("unknown subcommand %q", args[0])
	}
}

func printUsage(output *os.File) {
	fmt.Fprintf(output, "usage:\n")
	fmt.Fprintf(output, "  %s [--name <namespace> | --path <netns-path>]\n", os.Args[0])
	fmt.Fprintf(output, "  %s netns [--name <namespace> | --path <netns-path>]\n", os.Args[0])
	fmt.Fprintf(output, "  %s veth --name <namespace> --host-ifname <ifname> --peer-ifname <ifname> --host-ip <cidr> --peer-ip <cidr>\n", os.Args[0])
	fmt.Fprintf(output, "  %s server --name <namespace> --pid <child-pid> --listen-ip <ip> [--port 8080]\n", os.Args[0])
}
