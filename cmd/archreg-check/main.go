// Command archreg-check runs the rules engine from the command line:
//
//	archreg-check -rules ../rules/boverket-2026.json input.json
//	archreg-check -validate ../rules/*.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/martin/archreg/engine/rules"
)

func main() {
	rulesPath := flag.String("rules", "", "rule set JSON file")
	validate := flag.Bool("validate", false, "only validate the rule files given as arguments")
	flag.Parse()

	if *validate {
		ok := true
		for _, p := range flag.Args() {
			b, err := os.ReadFile(p)
			if err == nil {
				var rs rules.RuleSet
				if err = json.Unmarshal(b, &rs); err == nil {
					err = rules.Validate(&rs)
				}
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", p, err)
				ok = false
			} else {
				fmt.Printf("%s: ok\n", p)
			}
		}
		if !ok {
			os.Exit(1)
		}
		return
	}

	var in []byte
	var err error
	if flag.NArg() == 0 || flag.Arg(0) == "-" {
		in, err = io.ReadAll(os.Stdin)
	} else {
		in, err = os.ReadFile(flag.Arg(0))
	}
	must(err)
	var rs []byte
	if *rulesPath != "" {
		rs, err = os.ReadFile(*rulesPath)
		must(err)
	}
	out, err := rules.CheckJSON(in, rs)
	must(err)
	os.Stdout.Write(out)
	fmt.Println()
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
