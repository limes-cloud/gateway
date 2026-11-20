// Package matcher implements the gateway route/rewrite match expression DSL.
package matcher

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
)

const maxExpressionLength = 4096

// Expression is a compiled boolean match expression.
type Expression struct {
	source string
	root   node
}

// Source returns the original expression source.
func (e *Expression) Source() string {
	if e == nil {
		return ""
	}
	return e.source
}

// Eval evaluates the expression against an HTTP request.
func (e *Expression) Eval(req *http.Request) bool {
	if e == nil || e.root == nil || req == nil {
		return false
	}
	return e.root.eval(req)
}

// Compile parses and validates an expression.
func Compile(source string) (*Expression, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, fmt.Errorf("match expression is required")
	}
	if len(source) > maxExpressionLength {
		return nil, fmt.Errorf("match expression too long: max %d bytes", maxExpressionLength)
	}
	p := &parser{lex: newLexer(source)}
	root, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.lastErr != nil {
		return nil, p.lastErr
	}
	if tok := p.peek(); tok.kind != tokenEOF {
		return nil, fmt.Errorf("unexpected token %q at position %d", tok.text, tok.pos)
	}
	return &Expression{source: source, root: root}, nil
}

type tokenKind int

const (
	tokenEOF tokenKind = iota
	tokenIdent
	tokenString
	tokenLParen
	tokenRParen
	tokenComma
	tokenAnd
	tokenOr
	tokenNot
)

type token struct {
	kind tokenKind
	text string
	pos  int
}

type lexer struct {
	src string
	pos int
}

func newLexer(src string) *lexer { return &lexer{src: src} }

func (l *lexer) next() (token, error) {
	for l.pos < len(l.src) && isSpace(l.src[l.pos]) {
		l.pos++
	}
	if l.pos >= len(l.src) {
		return token{kind: tokenEOF, pos: l.pos}, nil
	}
	start := l.pos
	switch c := l.src[l.pos]; {
	case c == '(':
		l.pos++
		return token{kind: tokenLParen, text: "(", pos: start}, nil
	case c == ')':
		l.pos++
		return token{kind: tokenRParen, text: ")", pos: start}, nil
	case c == ',':
		l.pos++
		return token{kind: tokenComma, text: ",", pos: start}, nil
	case c == '!':
		l.pos++
		return token{kind: tokenNot, text: "!", pos: start}, nil
	case c == '&':
		if l.pos+1 < len(l.src) && l.src[l.pos+1] == '&' {
			l.pos += 2
			return token{kind: tokenAnd, text: "&&", pos: start}, nil
		}
		return token{}, fmt.Errorf("unexpected character '&' at position %d", start)
	case c == '|':
		if l.pos+1 < len(l.src) && l.src[l.pos+1] == '|' {
			l.pos += 2
			return token{kind: tokenOr, text: "||", pos: start}, nil
		}
		return token{}, fmt.Errorf("unexpected character '|' at position %d", start)
	case c == '"':
		return l.nextString()
	case isIdentStart(c):
		l.pos++
		for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
			l.pos++
		}
		return token{kind: tokenIdent, text: l.src[start:l.pos], pos: start}, nil
	default:
		return token{}, fmt.Errorf("unexpected character %q at position %d", c, start)
	}
}

func (l *lexer) nextString() (token, error) {
	start := l.pos
	l.pos++ // opening quote
	var b strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == '"' {
			l.pos++
			return token{kind: tokenString, text: b.String(), pos: start}, nil
		}
		if c == '\\' {
			if l.pos+1 >= len(l.src) {
				return token{}, fmt.Errorf("unterminated escape at position %d", l.pos)
			}
			next := l.src[l.pos+1]
			switch next {
			case '"', '\\', '/':
				b.WriteByte(next)
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			default:
				return token{}, fmt.Errorf("unsupported escape \\%c at position %d", next, l.pos)
			}
			l.pos += 2
			continue
		}
		b.WriteByte(c)
		l.pos++
	}
	return token{}, fmt.Errorf("unterminated string starting at position %d", start)
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

type parser struct {
	lex     *lexer
	cur     token
	hasCur  bool
	lastErr error
}

func (p *parser) peek() token {
	if !p.hasCur {
		tok, err := p.lex.next()
		if err != nil {
			p.lastErr = err
			tok = token{kind: tokenEOF, pos: p.lex.pos}
		}
		p.cur = tok
		p.hasCur = true
	}
	return p.cur
}

func (p *parser) take() token {
	tok := p.peek()
	p.hasCur = false
	return tok
}

func (p *parser) parseOr() (node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		if p.lastErr != nil {
			return nil, p.lastErr
		}
		if p.peek().kind != tokenOr {
			return left, nil
		}
		p.take()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = binaryNode{op: tokenOr, left: left, right: right}
	}
}

func (p *parser) parseAnd() (node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		if p.lastErr != nil {
			return nil, p.lastErr
		}
		if p.peek().kind != tokenAnd {
			return left, nil
		}
		p.take()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = binaryNode{op: tokenAnd, left: left, right: right}
	}
}

func (p *parser) parseUnary() (node, error) {
	if p.lastErr != nil {
		return nil, p.lastErr
	}
	if p.peek().kind == tokenNot {
		p.take()
		child, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return notNode{child: child}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (node, error) {
	if p.lastErr != nil {
		return nil, p.lastErr
	}
	tok := p.peek()
	switch tok.kind {
	case tokenLParen:
		p.take()
		expr, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tokenRParen {
			return nil, fmt.Errorf("expected ')' at position %d", p.peek().pos)
		}
		p.take()
		return expr, nil
	case tokenIdent:
		p.take()
		if p.peek().kind != tokenLParen {
			return nil, fmt.Errorf("expected '(' after %q at position %d", tok.text, p.peek().pos)
		}
		p.take()
		args, err := p.parseArgs()
		if err != nil {
			return nil, err
		}
		return newCallNode(tok.text, args, tok.pos)
	default:
		return nil, fmt.Errorf("unexpected token %q at position %d", tok.text, tok.pos)
	}
}

func (p *parser) parseArgs() ([]arg, error) {
	if p.peek().kind == tokenRParen {
		p.take()
		return nil, nil
	}
	args := make([]arg, 0, 4)
	for {
		if p.lastErr != nil {
			return nil, p.lastErr
		}
		tok := p.peek()
		switch tok.kind {
		case tokenString:
			p.take()
			args = append(args, arg{kind: argString, str: tok.text})
		case tokenIdent:
			p.take()
			switch strings.ToLower(tok.text) {
			case "true":
				args = append(args, arg{kind: argBool, boolVal: true})
			case "false":
				args = append(args, arg{kind: argBool, boolVal: false})
			default:
				return nil, fmt.Errorf("unexpected identifier %q in argument list at position %d", tok.text, tok.pos)
			}
		default:
			return nil, fmt.Errorf("unexpected token %q in argument list at position %d", tok.text, tok.pos)
		}
		switch p.peek().kind {
		case tokenComma:
			p.take()
			continue
		case tokenRParen:
			p.take()
			return args, nil
		default:
			return nil, fmt.Errorf("expected ',' or ')' at position %d", p.peek().pos)
		}
	}
}

type argKind int

const (
	argString argKind = iota
	argBool
)

type arg struct {
	kind    argKind
	str     string
	boolVal bool
}

type node interface {
	eval(*http.Request) bool
}

type binaryNode struct {
	op          tokenKind
	left, right node
}

func (n binaryNode) eval(req *http.Request) bool {
	if n.op == tokenAnd {
		return n.left.eval(req) && n.right.eval(req)
	}
	return n.left.eval(req) || n.right.eval(req)
}

type notNode struct{ child node }

func (n notNode) eval(req *http.Request) bool { return !n.child.eval(req) }

type callNode struct {
	name    string
	args    []arg
	ipAddrs []netip.Addr
	ipNets  []netip.Prefix
}

func newCallNode(name string, args []arg, pos int) (node, error) {
	n := &callNode{name: strings.ToLower(name), args: args}
	if err := n.validate(pos); err != nil {
		return nil, err
	}
	return n, nil
}

func (n *callNode) validate(pos int) error {
	switch n.name {
	case "req_host_in", "req_host_suffix_in", "req_host_port_in", "req_method_in",
		"req_header_key_in", "req_ua_contains_any", "req_query_key_in", "req_cookie_key_in":
		if len(n.args) == 0 {
			return fmt.Errorf("%s requires at least one string argument at position %d", n.name, pos)
		}
		return requireStrings(n.name, n.args)
	case "req_path_in":
		if len(n.args) == 0 {
			return fmt.Errorf("%s requires at least one string argument at position %d", n.name, pos)
		}
		return requireStrings(n.name, n.args)
	case "req_path_prefix_in", "req_path_suffix_in", "req_path_contain",
		"req_query_key_prefix_in":
		if len(n.args) < 1 || len(n.args) > 2 {
			return fmt.Errorf("%s expects 1 or 2 arguments at position %d", n.name, pos)
		}
		if n.args[0].kind != argString {
			return fmt.Errorf("%s first argument must be a string at position %d", n.name, pos)
		}
		if len(n.args) == 2 && n.args[1].kind != argBool {
			return fmt.Errorf("%s second argument must be true/false at position %d", n.name, pos)
		}
		return nil
	case "req_header_value_in", "req_query_value_in", "req_cookie_value_in":
		if len(n.args) < 2 {
			return fmt.Errorf("%s requires a name and at least one value at position %d", n.name, pos)
		}
		return requireStrings(n.name, n.args)
	case "req_header_value_contain", "req_header_value_prefix_in", "req_header_value_suffix_in",
		"req_query_value_contain", "req_query_value_prefix_in", "req_query_value_suffix_in":
		if len(n.args) < 2 || len(n.args) > 3 {
			return fmt.Errorf("%s expects 2 or 3 arguments at position %d", n.name, pos)
		}
		if n.args[0].kind != argString || n.args[1].kind != argString {
			return fmt.Errorf("%s first two arguments must be strings at position %d", n.name, pos)
		}
		if len(n.args) == 3 && n.args[2].kind != argBool {
			return fmt.Errorf("%s third argument must be true/false at position %d", n.name, pos)
		}
		return nil
	case "req_query_exist":
		if len(n.args) != 1 || n.args[0].kind != argString {
			return fmt.Errorf("%s expects one string argument at position %d", n.name, pos)
		}
		return nil
	case "req_client_ip_in", "req_client_ip_range":
		if len(n.args) == 0 {
			return fmt.Errorf("%s requires at least one CIDR/IP argument at position %d", n.name, pos)
		}
		if err := requireStrings(n.name, n.args); err != nil {
			return err
		}
		for _, raw := range n.args {
			value := strings.TrimSpace(raw.str)
			if prefix, err := netip.ParsePrefix(value); err == nil {
				n.ipNets = append(n.ipNets, prefix)
				continue
			}
			if addr, err := netip.ParseAddr(value); err == nil {
				n.ipAddrs = append(n.ipAddrs, addr)
				continue
			}
			return fmt.Errorf("%s invalid IP/CIDR %q at position %d", n.name, value, pos)
		}
		return nil
	case "req_proto_secure":
		if len(n.args) != 0 {
			return fmt.Errorf("%s expects no arguments at position %d", n.name, pos)
		}
		return nil
	default:
		return fmt.Errorf("unknown match function %q at position %d", n.name, pos)
	}
}

func requireStrings(name string, args []arg) error {
	for _, arg := range args {
		if arg.kind != argString {
			return fmt.Errorf("%s arguments must be strings", name)
		}
	}
	return nil
}

func (n *callNode) eval(req *http.Request) bool {
	switch n.name {
	case "req_host_in":
		host := requestHost(req)
		for _, value := range n.stringArgs() {
			if strings.EqualFold(host, value) {
				return true
			}
		}
		return false
	case "req_host_suffix_in":
		host := strings.ToLower(requestHost(req))
		for _, value := range n.stringArgs() {
			value = strings.ToLower(strings.TrimSpace(value))
			if value == "" {
				continue
			}
			if host == value || strings.HasSuffix(host, "."+value) {
				return true
			}
		}
		return false
	case "req_host_port_in":
		_, port := splitHostPort(req.Host)
		for _, value := range n.stringArgs() {
			if port == strings.TrimSpace(value) {
				return true
			}
		}
		return false
	case "req_path_in":
		path := requestPath(req)
		for _, value := range n.stringArgs() {
			if path == value {
				return true
			}
		}
		return false
	case "req_path_prefix_in":
		return matchText(requestPath(req), n.args[0].str, n.optionalBool(1), strings.HasPrefix)
	case "req_path_suffix_in":
		return matchText(requestPath(req), n.args[0].str, n.optionalBool(1), strings.HasSuffix)
	case "req_path_contain":
		return matchText(requestPath(req), n.args[0].str, n.optionalBool(1), strings.Contains)
	case "req_method_in":
		method := strings.ToUpper(req.Method)
		for _, value := range n.stringArgs() {
			if method == strings.ToUpper(strings.TrimSpace(value)) {
				return true
			}
		}
		return false
	case "req_header_key_in":
		for _, value := range n.stringArgs() {
			for key := range req.Header {
				if strings.EqualFold(key, value) {
					return true
				}
			}
		}
		return false
	case "req_header_value_in":
		values := req.Header.Values(n.args[0].str)
		for _, target := range n.stringArgsFrom(1) {
			for _, value := range values {
				if value == target {
					return true
				}
			}
		}
		return false
	case "req_header_value_contain":
		return matchHeader(req, n.args[0].str, n.args[1].str, n.optionalBool(2), strings.Contains)
	case "req_header_value_prefix_in":
		return matchHeader(req, n.args[0].str, n.args[1].str, n.optionalBool(2), strings.HasPrefix)
	case "req_header_value_suffix_in":
		return matchHeader(req, n.args[0].str, n.args[1].str, n.optionalBool(2), strings.HasSuffix)
	case "req_ua_contains_any":
		ua := req.Header.Get("User-Agent")
		for _, value := range n.stringArgs() {
			if strings.Contains(strings.ToLower(ua), strings.ToLower(value)) {
				return true
			}
		}
		return false
	case "req_query_exist":
		_, ok := req.URL.Query()[n.args[0].str]
		return ok
	case "req_query_key_in":
		query := req.URL.Query()
		for _, value := range n.stringArgs() {
			if _, ok := query[value]; ok {
				return true
			}
		}
		return false
	case "req_query_key_prefix_in":
		query := req.URL.Query()
		for key := range query {
			if matchText(key, n.args[0].str, n.optionalBool(1), strings.HasPrefix) {
				return true
			}
		}
		return false
	case "req_query_value_in":
		values := req.URL.Query()[n.args[0].str]
		for _, target := range n.stringArgsFrom(1) {
			for _, value := range values {
				if value == target {
					return true
				}
			}
		}
		return false
	case "req_query_value_contain":
		return matchQueryValue(req, n.args[0].str, n.args[1].str, n.optionalBool(2), strings.Contains)
	case "req_query_value_prefix_in":
		return matchQueryValue(req, n.args[0].str, n.args[1].str, n.optionalBool(2), strings.HasPrefix)
	case "req_query_value_suffix_in":
		return matchQueryValue(req, n.args[0].str, n.args[1].str, n.optionalBool(2), strings.HasSuffix)
	case "req_cookie_key_in":
		for _, value := range n.stringArgs() {
			if _, err := req.Cookie(value); err == nil {
				return true
			}
		}
		return false
	case "req_cookie_value_in":
		cookie, err := req.Cookie(n.args[0].str)
		if err != nil {
			return false
		}
		for _, target := range n.stringArgsFrom(1) {
			if cookie.Value == target {
				return true
			}
		}
		return false
	case "req_client_ip_in", "req_client_ip_range":
		addr, ok := remoteAddr(req)
		if !ok {
			return false
		}
		for _, candidate := range n.ipAddrs {
			if addr == candidate {
				return true
			}
		}
		for _, prefix := range n.ipNets {
			if prefix.Contains(addr) {
				return true
			}
		}
		return false
	case "req_proto_secure":
		return isSecure(req)
	default:
		return false
	}
}

func (n *callNode) stringArgs() []string { return n.stringArgsFrom(0) }

func (n *callNode) stringArgsFrom(start int) []string {
	out := make([]string, 0, len(n.args)-start)
	for i := start; i < len(n.args); i++ {
		out = append(out, n.args[i].str)
	}
	return out
}

func (n *callNode) optionalBool(index int) bool {
	if index >= 0 && index < len(n.args) && n.args[index].kind == argBool {
		return n.args[index].boolVal
	}
	return false
}

func matchText(value, target string, ignoreCase bool, fn func(string, string) bool) bool {
	if ignoreCase {
		return fn(strings.ToLower(value), strings.ToLower(target))
	}
	return fn(value, target)
}

func matchHeader(req *http.Request, header, target string, ignoreCase bool, fn func(string, string) bool) bool {
	for _, value := range req.Header.Values(header) {
		if matchText(value, target, ignoreCase, fn) {
			return true
		}
	}
	return false
}

func matchQueryValue(req *http.Request, key, target string, ignoreCase bool, fn func(string, string) bool) bool {
	for _, value := range req.URL.Query()[key] {
		if matchText(value, target, ignoreCase, fn) {
			return true
		}
	}
	return false
}

func requestHost(req *http.Request) string {
	host, _ := splitHostPort(req.Host)
	return host
}

func splitHostPort(hostPort string) (string, string) {
	hostPort = strings.TrimSpace(hostPort)
	if host, port, err := net.SplitHostPort(hostPort); err == nil {
		return host, port
	}
	if strings.HasPrefix(hostPort, "[") && strings.HasSuffix(hostPort, "]") {
		return strings.Trim(hostPort, "[]"), ""
	}
	return hostPort, ""
}

func requestPath(req *http.Request) string {
	if req.URL == nil || req.URL.Path == "" {
		return "/"
	}
	return req.URL.Path
}

func remoteAddr(req *http.Request) (netip.Addr, bool) {
	for _, value := range req.Header.Values("X-Forwarded-For") {
		for _, candidate := range strings.Split(value, ",") {
			if addr, ok := parseClientAddr(candidate); ok {
				return addr, true
			}
		}
	}
	if addr, ok := parseClientAddr(req.Header.Get("X-Real-IP")); ok {
		return addr, true
	}
	return parseClientAddr(req.RemoteAddr)
}

func parseClientAddr(raw string) (netip.Addr, bool) {
	host := strings.TrimSpace(raw)
	if host == "" || strings.EqualFold(host, "unknown") {
		return netip.Addr{}, false
	}
	if value, _, err := net.SplitHostPort(host); err == nil {
		host = value
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func isSecure(req *http.Request) bool {
	if req.TLS != nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(req.Header.Get("X-Forwarded-Proto")), "https")
}

// ParsePort is kept for expression diagnostics and unit tests.
func ParsePort(raw string) (int, bool) {
	port, err := strconv.Atoi(raw)
	if err != nil || port < 0 || port > 65535 {
		return 0, false
	}
	return port, true
}
