// Command testsummary reads `go test -json` on stdin and prints, per
// package, how many tests ran, passed, failed and were skipped (every test
// and subtest counts once), then the output of anything that failed.
// scripts/test_integration.sh uses it so an integration run that quietly
// skipped its database tests is visible, not just green. Exit status: 1 if
// any test failed or a package did not build.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

type event struct {
	Action  string
	Package string
	Test    string
	Output  string
}

type counts struct{ pass, fail, skip int }

const modulePrefix = "github.com/codercollo/willcoll/backend/"

func main() {
	byPkg := map[string]*counts{}
	buildFailed := map[string]bool{}
	output := map[[2]string][]string{} // {package, test} -> output lines; test "" = package-level

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var e event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		c := byPkg[e.Package]
		if c == nil {
			c = &counts{}
			byPkg[e.Package] = c
		}
		if e.Action == "output" {
			k := [2]string{e.Package, e.Test}
			output[k] = append(output[k], e.Output)
			continue
		}
		if e.Test == "" {
			if e.Action == "fail" {
				buildFailed[e.Package] = true
			}
			continue
		}
		switch e.Action {
		case "pass":
			c.pass++
		case "fail":
			c.fail++
		case "skip":
			c.skip++
		}
	}

	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "testsummary: reading input:", err)
		os.Exit(1)
	}

	names := make([]string, 0, len(byPkg))
	for n := range byPkg {
		names = append(names, n)
	}
	sort.Strings(names)

	var total counts
	failed := false
	fmt.Printf("\n%-62s %6s %6s %6s %6s\n", "package", "run", "pass", "fail", "skip")
	for _, n := range names {
		c := byPkg[n]
		if c.pass+c.fail+c.skip == 0 && !buildFailed[n] {
			continue // no tests in this package
		}
		mark := ""
		if c.fail > 0 || buildFailed[n] {
			mark, failed = "  FAIL", true
		}
		fmt.Printf("%-62s %6d %6d %6d %6d%s\n", strings.TrimPrefix(n, modulePrefix), c.pass+c.fail, c.pass, c.fail, c.skip, mark)
		total.pass += c.pass
		total.fail += c.fail
		total.skip += c.skip
	}
	fmt.Printf("%-62s %6d %6d %6d %6d\n", "TOTAL", total.pass+total.fail, total.pass, total.fail, total.skip)

	if !failed {
		return
	}
	fmt.Println("\nFAILURES")
	keys := make([][2]string, 0, len(output))
	for k := range output {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	for _, k := range keys {
		text := strings.Join(output[k], "")
		// Package-level output only for a package that failed to build/run;
		// test output only for a test that actually failed.
		if (k[1] == "" && buildFailed[k[0]]) || (k[1] != "" && strings.Contains(text, "--- FAIL")) {
			fmt.Printf("--- %s %s\n%s\n", strings.TrimPrefix(k[0], modulePrefix), k[1], text)
		}
	}
	os.Exit(1)
}
