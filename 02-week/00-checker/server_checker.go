package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

const defaultServerCheckTimeout = 5 * time.Second

type serverCheckRequest struct {
	NamespaceName string
	NamespacePath string
	PID           int
	ListenIP      string
	Port          int
}

type namespaceIdentity struct {
	Dev uint64
	Ino uint64
}

type echoServerResponse struct {
	Message      string `json:"message"`
	PID          int    `json:"pid"`
	NamespaceRef string `json:"namespace_ref"`
	LocalAddr    string `json:"local_addr"`
	RemoteAddr   string `json:"remote_addr"`
	Method       string `json:"method"`
	Path         string `json:"path"`
	ReceivedAt   string `json:"received_at"`
}

type serverCheckResult struct {
	HostNamespace  namespaceIdentity
	ChildNamespace namespaceIdentity
	NamedNamespace namespaceIdentity
	ChildRef       string
	EchoResponse   echoServerResponse
}

func runServerCheck(args []string) error {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	request := serverCheckRequest{
		Port: 8080,
	}
	fs.StringVar(&request.NamespaceName, "name", "", "named network namespace that should contain the server process")
	fs.StringVar(&request.NamespacePath, "path", "", "full path to the named network namespace mount")
	fs.IntVar(&request.PID, "pid", 0, "child process PID returned by /netns/{name}/exec")
	fs.StringVar(&request.ListenIP, "listen-ip", "", "IP address that the echo server should answer on")
	fs.IntVar(&request.Port, "port", request.Port, "TCP port that the echo server should answer on")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "usage: %s server --name <namespace> --pid <child-pid> --listen-ip <ip> [--port 8080]\n", os.Args[0])
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() != 0 {
		fs.Usage()
		return errors.New("unexpected positional arguments")
	}

	if err := validateServerCheckRequest(&request); err != nil {
		return err
	}

	if err := checkServerExecution(request); err != nil {
		return err
	}

	fmt.Printf(
		"server execution verified: pid=%d namespace=%s listen=%s\n",
		request.PID,
		request.NamespacePath,
		net.JoinHostPort(request.ListenIP, strconv.Itoa(request.Port)),
	)
	return nil
}

func validateServerCheckRequest(request *serverCheckRequest) error {
	if request.NamespacePath == "" {
		if request.NamespaceName == "" {
			return errors.New("either --name or --path must be provided")
		}

		request.NamespacePath = filepath.Join(namedNamespaceDir, request.NamespaceName)
	} else if request.NamespaceName == "" {
		request.NamespaceName = filepath.Base(request.NamespacePath)
	}

	if request.PID <= 0 {
		return errors.New("pid must be greater than zero")
	}

	if request.ListenIP == "" {
		return errors.New("listen-ip is required")
	}

	if parsedIP := net.ParseIP(request.ListenIP); parsedIP == nil {
		return fmt.Errorf("listen-ip must be a valid IP address: %q", request.ListenIP)
	}

	if request.Port < 1 || request.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}

	return nil
}

func checkServerExecution(request serverCheckRequest) error {
	if err := checkNamedNetNSMount(request.NamespacePath); err != nil {
		return err
	}

	hostNamespace, err := namespaceIdentityFromPath("/proc/self/ns/net")
	if err != nil {
		return fmt.Errorf("read host network namespace identity: %w", err)
	}

	childNamespacePath := fmt.Sprintf("/proc/%d/ns/net", request.PID)
	childNamespace, err := namespaceIdentityFromPath(childNamespacePath)
	if err != nil {
		return fmt.Errorf("read child network namespace identity for pid %d: %w", request.PID, err)
	}

	namedNamespace, err := namespaceIdentityFromPath(request.NamespacePath)
	if err != nil {
		return fmt.Errorf("read named network namespace identity %q: %w", request.NamespacePath, err)
	}

	childRef, err := os.Readlink(childNamespacePath)
	if err != nil {
		return fmt.Errorf("read child network namespace reference for pid %d: %w", request.PID, err)
	}

	echoResponse, err := fetchEchoServerResponse(request.ListenIP, request.Port, defaultServerCheckTimeout)
	if err != nil {
		return err
	}

	return validateServerCheckResult(request, serverCheckResult{
		HostNamespace:  hostNamespace,
		ChildNamespace: childNamespace,
		NamedNamespace: namedNamespace,
		ChildRef:       childRef,
		EchoResponse:   echoResponse,
	})
}

func validateServerCheckResult(request serverCheckRequest, result serverCheckResult) error {
	if result.ChildNamespace == result.HostNamespace {
		return fmt.Errorf("child pid %d is still running in the host network namespace", request.PID)
	}

	if result.ChildNamespace != result.NamedNamespace {
		return fmt.Errorf("child pid %d is not running inside named network namespace %q", request.PID, request.NamespacePath)
	}

	if result.EchoResponse.PID != request.PID {
		return fmt.Errorf(
			"echo server responded from pid %d, but expected pid %d",
			result.EchoResponse.PID,
			request.PID,
		)
	}

	expectedLocalAddr := net.JoinHostPort(request.ListenIP, strconv.Itoa(request.Port))
	if result.EchoResponse.LocalAddr != expectedLocalAddr {
		return fmt.Errorf(
			"echo server responded on local address %q, but expected %q",
			result.EchoResponse.LocalAddr,
			expectedLocalAddr,
		)
	}

	if result.EchoResponse.NamespaceRef != result.ChildRef {
		return fmt.Errorf(
			"echo server namespace reference %q does not match child namespace %q",
			result.EchoResponse.NamespaceRef,
			result.ChildRef,
		)
	}

	return nil
}

func fetchEchoServerResponse(ip string, port int, timeout time.Duration) (echoServerResponse, error) {
	targetURL := fmt.Sprintf("http://%s/", net.JoinHostPort(ip, strconv.Itoa(port)))
	client := &http.Client{
		Timeout: 500 * time.Millisecond,
	}

	deadline := time.Now().Add(timeout)
	var lastErr error
	var decodedResponse echoServerResponse

	for time.Now().Before(deadline) {
		response, err := client.Get(targetURL)
		if err != nil {
			lastErr = err
			time.Sleep(200 * time.Millisecond)
			continue
		}

		func() {
			defer response.Body.Close()

			if response.StatusCode != http.StatusOK {
				lastErr = fmt.Errorf("GET %s returned status %s", targetURL, response.Status)
				return
			}

			decoded := echoServerResponse{}
			if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
				lastErr = fmt.Errorf("decode echo server response from %s: %w", targetURL, err)
				return
			}

			lastErr = nil
			decodedResponse = decoded
		}()

		if lastErr == nil {
			return decodedResponse, nil
		}

		time.Sleep(200 * time.Millisecond)
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("timed out waiting for %s", targetURL)
	}

	return echoServerResponse{}, fmt.Errorf("fetch echo server response from %s: %w", targetURL, lastErr)
}

func namespaceIdentityFromPath(path string) (namespaceIdentity, error) {
	info, err := os.Stat(path)
	if err != nil {
		return namespaceIdentity{}, err
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return namespaceIdentity{}, fmt.Errorf("unexpected stat type for %s", path)
	}

	return namespaceIdentity{
		Dev: uint64(stat.Dev),
		Ino: uint64(stat.Ino),
	}, nil
}
