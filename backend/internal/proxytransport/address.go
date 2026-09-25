package proxytransport

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

const parserVersion = "ticket-proxy-v2"

// Candidate deliberately has no exported credential or full address field.
// Selection is resolved by parsing the original draft again, never a client URL.
type Candidate struct {
	SelectionID    string `json:"selection_id"`
	Protocol       string `json:"protocol"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	UsernameMasked string `json:"username_masked,omitempty"`
	SourceLine     int    `json:"source_line"`
	SourceColumn   int    `json:"source_column"`
	Format         string `json:"format"`
	endpoint       string
	identity       string
}

type Issue struct {
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ParseResult struct {
	Candidates        []Candidate `json:"candidates"`
	Issues            []Issue     `json:"issues"`
	SelectionID       string      `json:"selection_id,omitempty"`
	SelectionRequired bool        `json:"selection_required"`
}

// SelectionError is safe to expose: neither its text nor its issues echo input.
type SelectionError struct {
	Code   string
	Result ParseResult
}

func (e *SelectionError) Error() string {
	switch e.Code {
	case "proxy_selection_required":
		return "Select one recognized proxy before saving or testing"
	case "proxy_selection_stale":
		return "The proxy selection no longer matches this input; parse and select again"
	default:
		if len(e.Result.Issues) > 0 {
			issue := e.Result.Issues[0]
			return fmt.Sprintf("Line %d, column %d: %s", issue.Line, issue.Column, issue.Message)
		}
		return "Enter a valid proxy address"
	}
}

func validScheme(s string) bool {
	return s == "http" || s == "https" || s == "socks5" || s == "socks5h"
}

// ParseEndpoint accepts only a complete endpoint; transport code must not guess
// formats, apply protocol hints, or rewrite host casing and explicit ports.
func ParseEndpoint(raw string) (*url.URL, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || len(raw) > 16384 {
		return nil, errors.New("proxy endpoint is required")
	}
	u, err := url.Parse(raw)
	if err != nil || u == nil || !validScheme(u.Scheme) || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("proxy URL requires HTTP, HTTPS, SOCKS5 or SOCKS5H and no path, query or fragment")
	}
	host := u.Hostname()
	if !validHost(host) {
		return nil, errors.New("proxy host is missing or invalid; enclose IPv6 addresses in brackets")
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(u.Host, "[") {
		return nil, errors.New("enclose IPv6 addresses in brackets")
	}
	if port := u.Port(); port != "" {
		if _, err := parsePort(port); err != nil {
			return nil, err
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return nil, errors.New("proxy port must be between 1 and 65535")
	}
	return u, nil
}

func validHost(host string) bool {
	if host == "" {
		return false
	}
	if ip, zone, hasZone := strings.Cut(host, "%"); net.ParseIP(ip) != nil {
		return !hasZone || zone != "" && !strings.ContainsAny(zone, " /\\@[]:%\t\r\n")
	}
	for _, ch := range host {
		if !unicode.IsLetter(ch) && !unicode.IsDigit(ch) && ch != '-' && ch != '.' && ch != '_' {
			return false
		}
	}
	return !strings.HasPrefix(host, ".") && !strings.Contains(host, "..")
}

func parsePort(value string) (int, error) {
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, errors.New("proxy port must be between 1 and 65535")
		}
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > 65535 {
		return 0, errors.New("proxy port must be between 1 and 65535")
	}
	return n, nil
}

func effectivePort(u *url.URL) int {
	if p, err := strconv.Atoi(u.Port()); err == nil {
		return p
	}
	if u.Scheme == "http" {
		return 80
	}
	if u.Scheme == "https" {
		return 443
	}
	return 1080
}

// Parse is pure, bounded, and credential-safe. Independent grammars contribute
// candidates; a valid record is not lost because another record is malformed.
func Parse(raw, protocol string) ParseResult {
	protocolInput := protocol
	result := ParseResult{Candidates: []Candidate{}, Issues: []Issue{}}
	if len(raw) > 16384 {
		result.Issues = append(result.Issues, Issue{1, 1, "input", "input_too_long", "Proxy input must be at most 16384 bytes"})
		return result
	}
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol == "" {
		protocol = "http"
	}
	if !validScheme(protocol) {
		result.Issues = append(result.Issues, Issue{1, 1, "protocol", "unsupported_protocol", "Default protocol must be http, https, socks5 or socks5h"})
		return result
	}
	seen := map[string]bool{}
	add := func(items []Candidate) {
		for _, item := range items {
			if seen[item.identity] {
				continue
			}
			seen[item.identity] = true
			sum := sha256.Sum256([]byte(parserVersion + "\x00" + raw + "\x00" + protocolInput + "\x00" + item.identity))
			item.SelectionID = hex.EncodeToString(sum[:])
			result.Candidates = append(result.Candidates, item)
		}
	}
	var pending []labelValue
	pendingLine := 0
	flush := func() {
		if len(pending) == 0 {
			return
		}
		items, issues := parseLabels(pending, protocol, pendingLine)
		add(items)
		result.Issues = append(result.Issues, issues...)
		pending = nil
	}
	lines := strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n")
	for i, line := range strings.Split(lines, "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if strings.TrimSpace(line) == "(" || strings.TrimSpace(line) == ")" {
			continue
		}
		items, issue := parseRecord(line, protocol, i+1)
		labels := readLabels(line, i+1)
		authenticatedRecord := false
		for _, item := range items {
			authenticatedRecord = authenticatedRecord || item.UsernameMasked != ""
		}
		if len(labels) > 0 && (len(items) == 0 || !authenticatedRecord && (len(pending) > 0 || labels[0].key != "host")) {
			var host, port bool
			for _, label := range labels {
				host = host || label.key == "host"
				port = port || label.key == "port"
			}
			var previousHost, previousPort bool
			for _, label := range pending {
				previousHost = previousHost || label.key == "host"
				previousPort = previousPort || label.key == "port"
			}
			if host && port && previousHost && previousPort {
				flush()
			}
			if len(pending) == 0 {
				pendingLine = i + 1
			}
			pending = append(pending, labels...)
			continue
		}
		flush()
		if len(items) > 0 {
			add(items)
			continue
		}
		// Split multiple complete records only if no whole-record grammar
		// succeeded, so punctuation in an ordinary password stays literal.
		parts, splitErr := splitFields(line, columnDelimiter, true)
		if splitErr == nil && len(parts) > 1 {
			offset := 0
			for _, part := range parts {
				if at := strings.Index(line[offset:], part); at >= 0 {
					offset += at
				}
				column := len([]rune(line[:offset])) + 1
				items, issue := parseRecord(part, protocol, i+1)
				for n := range items {
					items[n].SourceColumn = column
				}
				add(items)
				if len(items) == 0 {
					issue.Column = column
					result.Issues = append(result.Issues, issue)
				}
				offset = min(len(line), offset+len(part))
			}
		} else {
			result.Issues = append(result.Issues, issue)
		}
	}
	flush()
	if len(result.Candidates) == 1 {
		result.SelectionID = result.Candidates[0].SelectionID
	}
	result.SelectionRequired = len(result.Candidates) > 1
	return result
}

// Resolve reuses the same parser for save and test. Even a unique candidate
// rejects a supplied stale selection. An empty draft is allowed only unselected.
func Resolve(raw, protocol, selectionID string) (string, error) {
	result := Parse(raw, protocol)
	if selectionID != "" {
		for _, candidate := range result.Candidates {
			if candidate.SelectionID == selectionID {
				return candidate.endpoint, nil
			}
		}
		return "", &SelectionError{Code: "proxy_selection_stale", Result: result}
	}
	if result.SelectionRequired {
		return "", &SelectionError{Code: "proxy_selection_required", Result: result}
	}
	if len(result.Candidates) == 1 {
		return result.Candidates[0].endpoint, nil
	}
	if strings.TrimSpace(raw) == "" && len(result.Issues) == 0 {
		return "", nil
	}
	return "", &SelectionError{Code: "invalid_proxy", Result: result}
}

func Normalize(raw string) (string, error) { return Resolve(raw, "", "") }

func candidate(raw, format string, line int) (Candidate, error) {
	u, err := ParseEndpoint(raw)
	if err != nil {
		return Candidate{}, err
	}
	user, password, present := "", "", false
	if u.User != nil {
		user = u.User.Username()
		password, present = u.User.Password()
	}
	// Length-prefixed credentials prevent separator collisions during dedupe.
	hostIdentity := strings.ToLower(u.Hostname())
	if host, zone, zoned := strings.Cut(u.Hostname(), "%"); net.ParseIP(host) != nil {
		hostIdentity = net.ParseIP(host).String()
		if zoned {
			hostIdentity += "%" + zone
		}
	}
	identity := fmt.Sprintf("%s|%s|%d|%t|%d:%s|%t|%d:%s", u.Scheme, hostIdentity, effectivePort(u), u.User != nil, len(user), user, present, len(password), password)
	masked := ""
	if u.User != nil {
		masked = "***"
	}
	return Candidate{Protocol: u.Scheme, Host: u.Hostname(), Port: effectivePort(u), UsernameMasked: masked, SourceLine: line, SourceColumn: 1, Format: format, endpoint: raw, identity: identity}, nil
}

func fieldsCandidate(fields []string, protocol, format string, line int, reverse bool) (Candidate, error) {
	host, port := fields[0], fields[1]
	username, password := "", ""
	if len(fields) == 4 {
		username, password = fields[2], fields[3]
		if reverse {
			username, password, host, port = fields[0], fields[1], fields[2], fields[3]
		}
	}
	if _, err := parsePort(port); err != nil {
		return Candidate{}, err
	}
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if !validHost(host) {
		return Candidate{}, errors.New("proxy host is missing or invalid")
	}
	u := &url.URL{Scheme: protocol, Host: net.JoinHostPort(host, port)}
	if len(fields) == 4 {
		u.User = url.UserPassword(username, password)
	}
	return candidate(u.String(), format, line)
}

func parseRecord(raw, protocol string, line int) ([]Candidate, Issue) {
	raw = strings.TrimSpace(raw)
	items := []Candidate{}
	issue := Issue{line, 1, "record", "invalid_record", "Expected a proxy URL, host and port, authenticated fields, or labelled fields"}
	if net.ParseIP(raw) != nil {
		issue.Field, issue.Code, issue.Message = "host", "ipv6_brackets_required", "Enclose IPv6 addresses in brackets and specify a port"
		return items, issue
	}
	add := func(item Candidate, err error) {
		if err == nil {
			items = append(items, item)
		}
	}
	if hasURIPrefix(raw) {
		item, err := candidate(raw, "uri", line)
		add(item, err)
		if err != nil {
			issue.Message = err.Error()
		}
		return items, issue
	}
	// URI userinfo is decoded exactly once by net/url; field grammars below
	// treat credentials literally, including percent signs and internal spaces.
	if u, err := ParseEndpoint(protocol + "://" + raw); err == nil && u.Port() != "" {
		format := "host:port"
		if u.User != nil {
			format = "username:password@host:port"
		}
		add(candidate(protocol+"://"+raw, format, line))
	}
	colon, err := splitFields(raw, func(r rune) bool { return r == ':' }, false)
	if err == nil {
		if len(colon) == 2 {
			add(fieldsCandidate(colon, protocol, "host:port", line, false))
		}
		if len(colon) >= 4 {
			add(fieldsCandidate([]string{colon[0], colon[1], colon[2], strings.Join(colon[3:], ":")}, protocol, "host:port:username:password", line, false))
			add(fieldsCandidate([]string{colon[0], strings.Join(colon[1:len(colon)-2], ":"), colon[len(colon)-2], colon[len(colon)-1]}, protocol, "username:password:host:port", line, true))
		}
	}
	for _, at := range delimiters(raw, func(r rune) bool { return r == '@' }) {
		hp, e1 := splitFields(raw[:at], func(r rune) bool { return r == ':' }, false)
		up, e2 := splitFields(raw[at+1:], func(r rune) bool { return r == ':' }, false)
		if e1 == nil && e2 == nil && len(hp) == 2 && len(up) >= 2 {
			add(fieldsCandidate([]string{hp[0], hp[1], up[0], strings.Join(up[1:], ":")}, protocol, "host:port@username:password", line, false))
		}
		if e1 == nil && e2 == nil && len(hp) >= 2 && len(up) == 2 && hasQuotedField(raw[:at]) {
			add(fieldsCandidate([]string{up[0], up[1], hp[0], strings.Join(hp[1:], ":")}, protocol, "quoted username:password@host:port", line, false))
		}
	}
	columns, err := splitFields(raw, columnDelimiter, true)
	if err == nil && (len(columns) == 2 || len(columns) == 4) {
		add(fieldsCandidate(columns, protocol, "host port username password", line, false))
		if len(columns) == 4 {
			add(fieldsCandidate(columns, protocol, "username password host port", line, true))
		}
	}
	if err != nil {
		issue.Code, issue.Message = "unclosed_quote", "Close the quoted proxy field"
	}
	if len(colon) == 2 && validHost(strings.Trim(colon[0], "[]")) {
		if _, err := parsePort(colon[1]); err != nil {
			issue.Field, issue.Code, issue.Message = "port", "invalid_port", err.Error()
		}
	}
	return items, issue
}

func hasURIPrefix(raw string) bool {
	end := strings.Index(raw, "://")
	if end <= 0 {
		return false
	}
	for i, r := range raw[:end] {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			continue
		}
		if i > 0 && (r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.') {
			continue
		}
		return false
	}
	return true
}

func hasQuotedField(raw string) bool {
	starts := []int{0}
	for _, position := range delimiters(raw, func(r rune) bool { return r == ':' }) {
		starts = append(starts, position+1)
	}
	for _, start := range starts {
		field := strings.TrimSpace(raw[start:])
		if strings.HasPrefix(field, "\"") || strings.HasPrefix(field, "'") {
			return true
		}
	}
	return false
}

func columnDelimiter(r rune) bool { return unicode.IsSpace(r) || r == ',' || r == '|' || r == ';' }

// delimiters ignores delimiters inside quoted fields and bracketed IPv6 hosts.
func delimiters(raw string, delimiter func(rune) bool) []int {
	var positions []int
	var quote rune
	brackets, escaped := 0, false
	for i, r := range raw {
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == '[' {
			brackets++
		}
		if r == ']' && brackets > 0 {
			brackets--
		}
		if brackets == 0 && delimiter(r) {
			positions = append(positions, i)
		}
	}
	return positions
}

func fieldValue(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if raw[0] != '\'' && raw[0] != '"' {
		return raw, nil
	}
	quote := raw[0]
	if len(raw) < 2 || raw[len(raw)-1] != quote {
		return "", errors.New("unclosed quoted field")
	}
	value := raw[1 : len(raw)-1]
	value = strings.ReplaceAll(value, "\\"+string(quote), string(quote))
	return value, nil
}

func splitFields(raw string, delimiter func(rune) bool, collapse bool) ([]string, error) {
	positions := delimiters(raw, delimiter)
	values := []string{}
	start := 0
	for _, at := range append(positions, len(raw)) {
		part := raw[start:at]
		value, err := fieldValue(part)
		if err != nil {
			return nil, err
		}
		if !collapse || value != "" || strings.HasPrefix(strings.TrimSpace(part), "\"") || strings.HasPrefix(strings.TrimSpace(part), "'") {
			values = append(values, value)
		}
		if at < len(raw) {
			_, width := runeAt(raw[at:])
			start = at + width
		}
	}
	return values, nil
}

func runeAt(raw string) (rune, int) {
	for _, r := range raw {
		return r, len(string(r))
	}
	return 0, 0
}

type labelValue struct {
	key, value   string
	line, column int
}

var labelNames = map[string]string{
	"proxy server": "host", "proxy_server": "host", "server": "host", "host": "host", "hostname": "host", "address": "host", "ip": "host", "主机": "host", "服务器": "host", "节点": "host", "代理服务器": "host", "代理地址": "host", "地址": "host",
	"port": "port", "端口": "port", "user": "username", "username": "username", "用户名": "username", "账号": "username", "帐号": "username",
	"pass": "password", "password": "password", "密码": "password", "scheme": "scheme", "protocol": "scheme", "协议": "scheme",
}

func readLabels(raw string, line int) []labelValue {
	// Quotes protect a password containing text such as "port: 8080" from
	// becoming another field. Colons and punctuation elsewhere stay untouched.
	starts := append([]int{0}, delimiters(raw, columnDelimiter)...)
	type found struct {
		start, value int
		key          string
	}
	var labels []found
	for _, position := range starts {
		if position > 0 {
			_, n := runeAt(raw[position:])
			position += n
		}
		rest := strings.TrimLeftFunc(raw[position:], unicode.IsSpace)
		position = len(raw) - len(rest)
		if len(labels) > 0 && position < labels[len(labels)-1].value {
			continue
		}
		at := strings.IndexAny(rest, ":：=")
		if at < 0 {
			continue
		}
		key, ok := labelNames[strings.ToLower(strings.TrimSpace(rest[:at]))]
		if !ok {
			continue
		}
		_, width := runeAt(rest[at:])
		if len(labels) > 0 && labels[len(labels)-1].start == position {
			continue
		}
		labels = append(labels, found{position, position + at + width, key})
	}
	if len(labels) == 0 || strings.TrimSpace(raw[:labels[0].start]) != "" {
		return nil
	}
	result := make([]labelValue, 0, len(labels))
	for i, label := range labels {
		end := len(raw)
		if i+1 < len(labels) {
			end = labels[i+1].start
		}
		value := raw[label.value:end]
		if i+1 < len(labels) {
			value = strings.TrimRightFunc(value, columnDelimiter)
		}
		result = append(result, labelValue{label.key, value, line, len([]rune(raw[:label.start])) + 1})
	}
	return result
}

func parseLabels(labels []labelValue, protocol string, line int) ([]Candidate, []Issue) {
	fields := map[string]string{}
	issues := []Issue{}
	for _, label := range labels {
		value, err := fieldValue(label.value)
		if err != nil {
			issues = append(issues, Issue{label.line, label.column, label.key, "unclosed_quote", "Close the quoted proxy field"})
			continue
		}
		if previous, exists := fields[label.key]; exists && previous != value {
			issues = append(issues, Issue{label.line, label.column, label.key, "conflicting_field", "Explicit values for the same proxy field conflict"})
		}
		fields[label.key] = value
	}
	if len(issues) > 0 {
		return nil, issues
	}
	fail := func(field, code, message string) ([]Candidate, []Issue) {
		return nil, []Issue{{line, 1, field, code, message}}
	}
	host := fields["host"]
	if host == "" {
		return fail("host", "missing_host", "Proxy host is required")
	}
	if explicit := strings.ToLower(fields["scheme"]); explicit != "" {
		if !validScheme(explicit) {
			return fail("scheme", "unsupported_protocol", "Protocol must be http, https, socks5 or socks5h")
		}
		protocol = explicit
	}
	var u *url.URL
	var err error
	if strings.Contains(host, "://") {
		u, err = ParseEndpoint(host)
		if err != nil {
			return fail("host", "invalid_endpoint", err.Error())
		}
		if explicit := strings.ToLower(fields["scheme"]); explicit != "" && explicit != u.Scheme {
			return fail("scheme", "conflicting_field", "The explicit protocol conflicts with the address protocol")
		}
	} else {
		hostPort, splitErr := splitFields(host, func(r rune) bool { return r == ':' }, false)
		if splitErr == nil && len(hostPort) == 2 {
			u = &url.URL{Scheme: protocol, Host: net.JoinHostPort(strings.Trim(hostPort[0], "[]"), hostPort[1])}
		} else {
			u = &url.URL{Scheme: protocol, Host: host}
		}
	}
	if port := fields["port"]; port != "" {
		if _, err := parsePort(port); err != nil {
			return fail("port", "invalid_port", err.Error())
		}
		if u.Port() != "" && u.Port() != port {
			return fail("port", "conflicting_field", "The explicit port conflicts with the address port")
		}
		u.Host = net.JoinHostPort(u.Hostname(), port)
	} else if u.Port() == "" && !strings.Contains(host, "://") {
		return fail("port", "missing_port", "Proxy port is required")
	}
	_, userPresent := fields["username"]
	_, passwordPresent := fields["password"]
	if userPresent != passwordPresent {
		return fail("credentials", "incomplete_credentials", "Both username and password fields are required")
	}
	if userPresent {
		if u.User != nil {
			password, present := u.User.Password()
			if u.User.Username() != fields["username"] || !present || password != fields["password"] {
				return fail("credentials", "conflicting_field", "Explicit credentials conflict with the address credentials")
			}
		}
		u.User = url.UserPassword(fields["username"], fields["password"])
	}
	item, err := candidate(u.String(), "labelled fields", line)
	if err != nil {
		return fail("record", "invalid_record", err.Error())
	}
	return []Candidate{item}, nil
}
