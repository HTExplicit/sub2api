package proxytransport

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Normalize accepts the existing URL, host:port:user:password and labelled
// multiline forms. It performs no DNS/network IO and never echoes credentials.
func Normalize(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if len(raw) > 16384 {
		return "", errors.New("proxy input is too long")
	}
	if strings.ContainsAny(raw, "\r\n") {
		fields := map[string]string{}
		for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || line == "(" || line == ")" {
				continue
			}
			i := strings.IndexAny(line, ":：=")
			if i < 0 {
				return "", errors.New("each proxy field needs a label")
			}
			key := strings.ToLower(strings.TrimSpace(line[:i]))
			value := strings.TrimSpace(line[i+len(string([]rune(line[i:])[0])):])
			switch key {
			case "proxy server", "proxy_server", "server", "host", "hostname", "主机", "服务器", "节点", "代理服务器":
				key = "host"
			case "port", "端口":
				key = "port"
			case "user", "username", "用户名", "账号", "帐号":
				key = "username"
			case "pass", "password", "密码":
				key = "password"
			case "scheme", "protocol", "协议":
				key = "scheme"
			default:
				return "", errors.New("unrecognized proxy field label")
			}
			if _, exists := fields[key]; exists {
				return "", errors.New("duplicate proxy field")
			}
			fields[key] = value
		}
		if fields["host"] == "" || fields["port"] == "" {
			return "", errors.New("proxy host and port are required")
		}
		scheme := fields["scheme"]
		if scheme == "" {
			scheme = "http"
		}
		address := url.URL{Scheme: strings.ToLower(scheme), Host: net.JoinHostPort(strings.Trim(fields["host"], "[]"), fields["port"])}
		_, user := fields["username"]
		_, password := fields["password"]
		if user != password {
			return "", errors.New("both username and password are required")
		}
		if user {
			address.User = url.UserPassword(fields["username"], fields["password"])
		}
		raw = address.String()
	} else if !strings.Contains(raw, "://") {
		if strings.Contains(raw, "@") {
			raw = "http://" + raw
		} else {
			parts := strings.SplitN(raw, ":", 4)
			if len(parts) == 4 && !strings.HasPrefix(raw, "[") {
				if _, err := strconv.Atoi(parts[1]); err != nil {
					return "", errors.New("ambiguous proxy fields; use labelled fields")
				}
				address := url.URL{Scheme: "http", Host: net.JoinHostPort(parts[0], parts[1]), User: url.UserPassword(parts[2], parts[3])}
				raw = address.String()
			} else {
				raw = "http://" + raw
			}
		}
	}
	address, err := url.Parse(raw)
	if err != nil || address == nil {
		return "", errors.New("invalid proxy URL")
	}
	address.Scheme = strings.ToLower(address.Scheme)
	if address.Hostname() == "" || strings.ContainsAny(address.Hostname(), " \t\r\n") || address.Opaque != "" || address.RawQuery != "" || address.ForceQuery || address.Fragment != "" || (address.Path != "" && address.Path != "/") {
		return "", errors.New("proxy URL requires a host and no path, query or fragment")
	}
	switch address.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return "", errors.New("unsupported proxy protocol")
	}
	port, err := strconv.Atoi(address.Port())
	if err != nil || port < 1 || port > 65535 {
		return "", errors.New("proxy port must be between 1 and 65535")
	}
	address.Path = ""
	return address.String(), nil
}
