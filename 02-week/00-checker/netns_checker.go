package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const mountInfoPath = "/proc/self/mountinfo"

type mountInfoEntry struct {
	MountPoint  string
	FSType      string
	MountSource string
}

func runNetNSCheck(args []string) error {
	fs := flag.NewFlagSet("netns", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		name string
		path string
	)

	fs.StringVar(&name, "name", "", "named network namespace to verify")
	fs.StringVar(&path, "path", "", "full path to the named network namespace mount")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "usage: %s netns [--name <namespace> | --path <netns-path>]\n", os.Args[0])
		fmt.Fprintf(fs.Output(), "       %s [--name <namespace> | --path <netns-path>]\n", os.Args[0])
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() != 0 {
		fs.Usage()
		return errors.New("unexpected positional arguments")
	}

	if path == "" {
		if name == "" {
			fs.Usage()
			return errors.New("either --name or --path must be provided")
		}

		path = filepath.Join(namedNamespaceDir, name)
	}

	if err := checkNamedNetNSMount(path); err != nil {
		return err
	}

	fmt.Printf("namespace mount verified: %s uses nsfs\n", path)
	return nil
}

func checkNamedNetNSMount(path string) error {
	path = filepath.Clean(path)

	file, err := os.Open(mountInfoPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", mountInfoPath, err)
	}
	defer file.Close()

	entry, err := findMountInfoEntryByPath(file, path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s is not mounted according to %s", path, mountInfoPath)
		}

		return err
	}

	if entry.FSType != "nsfs" {
		return fmt.Errorf("%s is mounted, but filesystem type is %q instead of %q", path, entry.FSType, "nsfs")
	}

	return nil
}

func findMountInfoEntryByPath(r io.Reader, targetPath string) (mountInfoEntry, error) {
	targetPath = filepath.Clean(targetPath)

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		entry, err := parseMountInfoLine(scanner.Text())
		if err != nil {
			return mountInfoEntry{}, err
		}

		if sameMountPoint(entry.MountPoint, targetPath) {
			return entry, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return mountInfoEntry{}, fmt.Errorf("scan mountinfo: %w", err)
	}

	return mountInfoEntry{}, os.ErrNotExist
}

func sameMountPoint(left, right string) bool {
	return canonicalMountPoint(left) == canonicalMountPoint(right)
}

func canonicalMountPoint(path string) string {
	path = filepath.Clean(path)

	switch {
	case strings.HasPrefix(path, "/var/run/"):
		return "/run/" + strings.TrimPrefix(path, "/var/run/")
	case path == "/var/run":
		return "/run"
	default:
		return path
	}
}

func parseMountInfoLine(line string) (mountInfoEntry, error) {
	left, right, found := strings.Cut(line, " - ")
	if !found {
		return mountInfoEntry{}, fmt.Errorf("invalid mountinfo line: missing separator: %q", line)
	}

	leftFields := strings.Fields(left)
	if len(leftFields) < 5 {
		return mountInfoEntry{}, fmt.Errorf("invalid mountinfo line: too few fields before separator: %q", line)
	}

	rightFields := strings.Fields(right)
	if len(rightFields) < 3 {
		return mountInfoEntry{}, fmt.Errorf("invalid mountinfo line: too few fields after separator: %q", line)
	}

	mountPoint, err := unescapeMountInfoField(leftFields[4])
	if err != nil {
		return mountInfoEntry{}, fmt.Errorf("decode mount point %q: %w", leftFields[4], err)
	}

	return mountInfoEntry{
		MountPoint:  mountPoint,
		FSType:      rightFields[0],
		MountSource: rightFields[1],
	}, nil
}

func unescapeMountInfoField(value string) (string, error) {
	var builder strings.Builder
	builder.Grow(len(value))

	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			builder.WriteByte(value[i])
			continue
		}

		if i+3 >= len(value) {
			return "", fmt.Errorf("truncated escape sequence in %q", value)
		}

		octal := value[i+1 : i+4]
		parsed, err := strconv.ParseUint(octal, 8, 8)
		if err != nil {
			return "", fmt.Errorf("invalid octal escape %q in %q", octal, value)
		}

		builder.WriteByte(byte(parsed))
		i += 3
	}

	return builder.String(), nil
}
