package launchcode

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// EnvListenAddr is the xLauncher / xTools listen address env for xFacade.
const EnvListenAddr = "XFACADE_LISTEN_ADDR"

const defaultListenHost = "127.0.0.1"

// ParseListenAddr parses host:port or :port.
// Empty host becomes 127.0.0.1 so Launch API stays loopback-oriented.
func ParseListenAddr(raw string) (host string, port int, err error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", 0, fmt.Errorf("empty listen address")
	}
	lastColon := strings.LastIndex(s, ":")
	if lastColon < 0 {
		return "", 0, fmt.Errorf("invalid listen address: %s", s)
	}
	hostPart := s[:lastColon]
	portPart := s[lastColon+1:]
	portNum, convErr := strconv.Atoi(portPart)
	if convErr != nil || portNum < 1 || portNum > 65535 {
		return "", 0, fmt.Errorf("invalid port in listen address: %s", s)
	}
	host = strings.TrimSpace(hostPart)
	if host == "" {
		host = defaultListenHost
	}
	return host, portNum, nil
}

func addrFromFlag(argv []string) string {
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == "-addr" {
			if i+1 < len(argv) {
				return strings.TrimSpace(argv[i+1])
			}
			return ""
		}
		if strings.HasPrefix(arg, "-addr=") {
			return strings.TrimSpace(strings.TrimPrefix(arg, "-addr="))
		}
	}
	return ""
}

// ResolveLaunchListen resolves LaunchServer listen host/port.
// Precedence: -addr > XFACADE_LISTEN_ADDR > configPort on 127.0.0.1.
// When configPort <= 0 and no override is set, port 0 means "auto-assign".
func ResolveLaunchListen(argv []string, getenv func(string) string, configPort int) (host string, port int, err error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	raw := addrFromFlag(argv)
	if raw == "" {
		raw = strings.TrimSpace(getenv(EnvListenAddr))
	}
	if raw != "" {
		return ParseListenAddr(raw)
	}
	return defaultListenHost, configPort, nil
}
