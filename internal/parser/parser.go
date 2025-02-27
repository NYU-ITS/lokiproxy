package parser

import (
	"fmt"
	"log"
	"maps"
	"os"
	"strings"
)

func isWhiteSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

type queryParser struct {
	pos            int
	query          string
	result         strings.Builder
	requiredLabels map[string]interface{}
}

func debugLog(msg string, args ...any) {
	//log.Printf(msg, args...)
}

func (p *queryParser) outputByte(c byte) {
	debugLog("output '%c'", c)
	p.result.WriteByte(c)
}

func (p *queryParser) parse() error {
	debugLog("parse %#v pos=%v", p.query, p.pos)
	for {
		p.consumeWhiteSpace()
		debugLog("parse() after whitespace pos=%v", p.pos)
		if p.pos >= len(p.query) {
			return nil
		}
		c := p.query[p.pos]
		switch {
		case c == '{':
			p.outputByte(c)
			p.pos += 1
			if err := p.consumeSelectors(); err != nil {
				return err
			}
		case (c == '[' || c == ']' || c == '(') || c == ')' || c == ',' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' ||
			c == '=' || c == '<' || c == '>' || c == '!' || c == '|' || c == ':' ||
			c == '#' || c == '*' || c == '+' || c == '-' || c == '/' || c == '%' ||
			(c >= '0' && c <= '9'):
			p.outputByte(c)
			p.pos += 1
		case c == '"':
			p.outputByte(c)
			p.pos += 1
			if _, err := p.consumeString(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unexpected character %#v", c)
		}
	}
}

func (p *queryParser) consumeWhiteSpace() {
	for p.pos < len(p.query) && isWhiteSpace(p.query[p.pos]) {
		p.outputByte(p.query[p.pos])
		p.pos += 1
	}
}

func (p *queryParser) consumeString() (string, error) {
	debugLog("consumeString() pos=%v", p.pos)
	start := p.pos - 1
	for p.pos < len(p.query) {
		c := p.query[p.pos]
		p.outputByte(c)
		switch c {
		case '"':
			p.pos += 1
			debugLog("consumeString() done pos=%v", p.pos)
			return p.query[start:p.pos], nil
		case '\\':
			p.pos += 1
			if p.pos >= len(p.query) {
				return "", fmt.Errorf("missing closing string delimiter")
			}
			p.outputByte(p.query[p.pos])
		}
		p.pos += 1
	}
	return "", fmt.Errorf("missing closing string delimiter")
}

func (p *queryParser) consumeIdentifier() string {
	debugLog("consumeIdentifier() pos=%v", p.pos)
	start := p.pos
	for p.pos < len(p.query) {
		c := p.query[p.pos]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || (p.pos > start && c >= '0' && c <= '9') {
			p.outputByte(c)
			p.pos += 1
		} else {
			break
		}
	}
	debugLog("consumeIdentifier() done pos=%v result=%#v", p.pos, p.query[start:p.pos])
	return p.query[start:p.pos]
}

func (p *queryParser) consumeSelectors() error {
	debugLog("consumeSelectors() pos=%v", p.pos)
	missingLabels := maps.Clone(p.requiredLabels)
	insertComma := false
	for p.pos < len(p.query) {
		c := p.query[p.pos]
		if c == '}' {
			debugLog("inserting %v missing labels", len(missingLabels))
			for label := range missingLabels {
				if insertComma {
					p.result.WriteString(", ")
				}
				p.result.WriteString(label)
				insertComma = true
			}
			p.outputByte('}')
			p.pos += 1
			debugLog("consumeSelectors() done pos=%v", p.pos)
			return nil
		} else if c == ',' || c == ' ' {
			p.outputByte(c)
			p.pos += 1
		} else if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' {
			var label strings.Builder
			label.WriteString(p.consumeIdentifier())
			p.consumeWhiteSpace()
			if p.pos >= len(p.query) {
				return fmt.Errorf("missing selector operator")
			}
			for p.pos < len(p.query) {
				c := p.query[p.pos]
				p.outputByte(c)
				p.pos += 1
				if isWhiteSpace(c) {
					continue
				}
				if c == '"' {
					s, err := p.consumeString()
					if err != nil {
						return err
					}
					label.WriteString(s)
					delete(missingLabels, label.String())
					insertComma = true
					break
				}
				label.WriteByte(c)
			}
		} else {
			return fmt.Errorf("syntax error in selectors")
		}
	}
	return fmt.Errorf("missing closing brace")
}

func ProcessQuery(query string, requiredLabels map[string]interface{}) (string, error) {
	parser := queryParser{
		pos:            0,
		query:          query,
		requiredLabels: requiredLabels,
	}
	if err := parser.parse(); err != nil {
		return "", err
	}
	if os.Getenv("LOKIPROXY_DEBUG_SHOW_QUERIES") != "" {
		log.Printf("%#v -> %#v", query, parser.result.String())
	}
	return parser.result.String(), nil
}
