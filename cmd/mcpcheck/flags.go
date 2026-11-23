package main

import (
	"fmt"
	"net/http"
	"strings"
)

// headerFlag collects repeated --header "Name: value" flags.
type headerFlag http.Header

func (h *headerFlag) String() string { return "" }

func (h *headerFlag) Set(v string) error {
	name, value, ok := strings.Cut(v, ":")
	name = strings.TrimSpace(name)
	if !ok || name == "" {
		return fmt.Errorf("want \"Name: value\", got %q", v)
	}
	if *h == nil {
		*h = headerFlag{}
	}
	http.Header(*h).Add(name, strings.TrimSpace(value))
	return nil
}

// splitCommand splits a command line into argv the way a POSIX shell
// would for plain words, single quotes, double quotes, and backslash
// escapes. Expansions, pipes, and redirects are not supported; wrap the
// command in `sh -c '...'` for those.
func splitCommand(s string) ([]string, error) {
	var sp splitter
	for _, r := range s {
		sp.feed(r)
	}
	if sp.quote != 0 || sp.escaped {
		return nil, fmt.Errorf("unterminated quote or escape in %q", s)
	}
	sp.flush()
	if len(sp.argv) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	return sp.argv, nil
}

type splitter struct {
	argv    []string
	cur     strings.Builder
	inWord  bool
	escaped bool
	quote   rune
}

func (s *splitter) feed(r rune) {
	switch {
	case s.escaped:
		s.cur.WriteRune(r)
		s.escaped = false
	case s.quote != 0:
		s.feedQuoted(r)
	default:
		s.feedBare(r)
	}
}

func (s *splitter) feedQuoted(r rune) {
	switch {
	case r == s.quote:
		s.quote = 0
	case r == '\\' && s.quote == '"':
		s.escaped = true
	default:
		s.cur.WriteRune(r)
	}
}

func (s *splitter) feedBare(r rune) {
	switch r {
	case '\\':
		s.escaped, s.inWord = true, true
	case '\'', '"':
		s.quote, s.inWord = r, true
	case ' ', '\t', '\n':
		s.flush()
	default:
		s.cur.WriteRune(r)
		s.inWord = true
	}
}

func (s *splitter) flush() {
	if s.inWord {
		s.argv = append(s.argv, s.cur.String())
		s.cur.Reset()
		s.inWord = false
	}
}
