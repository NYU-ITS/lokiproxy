package parser

import (
	"bytes"
	"fmt"
	"testing"
)

func check(t *testing.T, original string, expected string, requiredLabels map[string]interface{}) {
	result, err := ProcessQuery(original, requiredLabels)
	if err != nil {
		t.Fatalf("parser error\nexpected: %#v\nerror   : %v", expected, err)
	}
	if result != expected {
		t.Fatalf("parser error\nexpected: %#v\nresult  : %#v", expected, result)
	}
}

func checkM(t *testing.T, original string, expected []string, requiredLabels map[string]interface{}) {
	result, err := ProcessQuery(original, requiredLabels)
	if err != nil {
		t.Fatalf("parser error\nexpected: %#v\nerror   : %v", expected, err)
	}
	for _, possible := range expected {
		if result == possible {
			return
		}
	}
	var out bytes.Buffer
	out.WriteString("parser error\n")
	for _, possible := range expected {
		fmt.Fprintf(&out, "expected: %#v\n", possible)
	}
	fmt.Fprintf(&out, "result  : %#v", result)
	t.Fatal(out.String())
}

func TestLogQuery(t *testing.T) {
	oneLabel := map[string]interface{}{"namespace=\"yes\"": nil}
	check(
		t,
		"{job_name=\"myapp\"} != ip(\"192.168.4.5-192.168.4.20\")",
		"{job_name=\"myapp\", namespace=\"yes\"} != ip(\"192.168.4.5-192.168.4.20\")",
		oneLabel,
	)
	check(
		t,
		"{job_name=\"myapp\", namespace=\"yes\"} != ip(\"192.168.4.5-192.168.4.20\")",
		"{job_name=\"myapp\", namespace=\"yes\"} != ip(\"192.168.4.5-192.168.4.20\")",
		oneLabel,
	)
	check(
		t,
		"{job_name=\"myapp\",namespace = \"yes\" } != ip(\"192.168.4.5-192.168.4.20\")",
		"{job_name=\"myapp\",namespace = \"yes\" } != ip(\"192.168.4.5-192.168.4.20\")",
		oneLabel,
	)

	oneNeg := map[string]interface{}{"namespace!=\"admin\"": nil}
	check(
		t,
		"{}",
		"{namespace!=\"admin\"}",
		oneNeg,
	)
	check(
		t,
		"{ namespace!=\"admin\" }",
		"{ namespace!=\"admin\" }",
		oneNeg,
	)

	twoLabels := map[string]interface{}{"job=\"pods\"": nil, "namespace=~\"yes|oui\"": nil}
	checkM(
		t,
		"{}",
		[]string{
			"{job=\"pods\", namespace=~\"yes|oui\"}",
			"{namespace=~\"yes|oui\", job=\"pods\"}",
		},
		twoLabels,
	)
	checkM(
		t,
		"{audit=~\".+\"}",
		[]string{
			"{audit=~\".+\", job=\"pods\", namespace=~\"yes|oui\"}",
			"{audit=~\".+\", namespace=~\"yes|oui\", job=\"pods\"}",
		},
		twoLabels,
	)
	check(
		t,
		"{  job=\"pods\" }",
		"{  job=\"pods\" , namespace=~\"yes|oui\"}",
		twoLabels,
	)
	check(
		t,
		"{ job=\"pods\" , audit!=\"true\" }",
		"{ job=\"pods\" , audit!=\"true\" , namespace=~\"yes|oui\"}",
		twoLabels,
	)
	check(
		t,
		"{job=\"pods\"} | line_format \"{{.namespace}}\"",
		"{job=\"pods\", namespace=~\"yes|oui\"} | line_format \"{{.namespace}}\"",
		map[string]interface{}{"namespace=~\"yes|oui\"": nil},
	)
}

func TestInternal(t *testing.T) {
	{
		parser := queryParser{
			pos:   1,
			query: "   {}",
		}
		parser.consumeWhiteSpace()
		if parser.pos != 3 {
			t.Fatalf("end of whitespace: %d, expected %d", parser.pos, 3)
		}
		expected := "  "
		if parser.result.String() != expected {
			t.Fatalf("parsed: %#v, expected %#v", parser.result.String(), expected)
		}
	}

	{
		parser := queryParser{
			pos:   3,
			query: "a=\"a long \\\"string\" here",
		}
		if _, err := parser.consumeString(); err != nil {
			t.Fatal(err)
		}
		if parser.pos != 19 {
			t.Fatalf("end of string: %d, expected %d", parser.pos, 19)
		}
		expected := "a long \\\"string\""
		if parser.result.String() != expected {
			t.Fatalf("parsed: %#v, expected %#v", parser.result.String(), expected)
		}
	}
}
