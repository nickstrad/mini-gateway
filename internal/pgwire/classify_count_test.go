//go:build clscount

package pgwire

import (
	"fmt"
	"strings"
	"testing"
)

const clsReadsGrowthFloor = clsWorkLargeInput/clsWorkSmallInput - 1

type clsCountedGroup struct {
	prefix string
	shapes []string
}

type clsCountedWork struct {
	name         string
	small, large int
}

func TestCountedWorkGrowsLinearly(t *testing.T) {
	groups := []clsCountedGroup{
		{"SELECT ", clsOperatorRunShapes},
		{"SELECT ", clsSpanKeywordShapes},
		{"", clsWholeCallShapes},
	}
	for _, g := range groups {
		for _, unit := range g.shapes {
			small, large := clsCountedSizes(g.prefix, unit)
			t.Logf("%q: 4 KiB %v, 64 KiB %v", unit, small, large)
			for _, p := range clsCountedProblems(clsCountedPairs(small, large)) {
				t.Errorf("%q: %s", unit, p)
			}
		}
	}
}

func TestCountedCheckRejectsWorkThatDoesNotGrowLinearly(t *testing.T) {
	healthy := clsCountedPairs(clsCountedSizes("SELECT ", "'x'"))
	if p := clsCountedProblems(healthy); len(p) != 0 {
		t.Fatalf("a healthy shape was rejected: %v", p)
	}
	for _, c := range []struct {
		name  string
		plant func(w []clsCountedWork)
	}{
		{"no lexer reads", func(w []clsCountedWork) { w[0].small, w[0].large = 0, 0 }},
		{"no token steps", func(w []clsCountedWork) { w[1].small, w[1].large = 0, 0 }},
		{"reads that do not grow with the input", func(w []clsCountedWork) { w[0].large = w[0].small }},
		{"quadratic reads", func(w []clsCountedWork) { w[0].large = 256 * w[0].small }},
		{"quadratic token visits", func(w []clsCountedWork) { w[len(w)-2].large = 256*w[len(w)-2].small + 1 }},
	} {
		planted := append([]clsCountedWork(nil), healthy...)
		c.plant(planted)
		if len(clsCountedProblems(planted)) == 0 {
			t.Errorf("%s: the check accepted %v", c.name, planted)
		}
	}
}

func clsCountedSizes(prefix, unit string) (clsCounts, clsCounts) {
	_, small := clsRunCounted([]byte(prefix+strings.Repeat(unit, clsWorkSmallInput/len(unit))), false, false)
	_, large := clsRunCounted([]byte(prefix+strings.Repeat(unit, clsWorkLargeInput/len(unit))), false, false)
	return small, large
}

func clsCountedPairs(small, large clsCounts) []clsCountedWork {
	return []clsCountedWork{
		{"lexer reads", small.lex.reads.value(), large.lex.reads.value()},
		{"token steps", small.lex.steps.value(), large.lex.steps.value()},
		{"operator-run bytes", small.lex.opRuns.value(), large.lex.opRuns.value()},
		{"lookahead bytes", small.lex.ahead.value(), large.lex.ahead.value()},
		{"keyword checks", small.keywordChecks, large.keywordChecks},
		{"token visits", small.tokenVisits, large.tokenVisits},
		{"literal bytes", small.literalBytes, large.literalBytes},
	}
}

func clsCountedProblems(w []clsCountedWork) []string {
	var out []string
	for i, c := range w {
		if i < 2 && c.small == 0 {
			out = append(out, fmt.Sprintf("%s did not move", c.name))
		}
		if i == 0 && c.large < clsReadsGrowthFloor*c.small {
			out = append(out, fmt.Sprintf("16 times the input gave %d %s against %d; want at least %d times as many", c.large, c.name, c.small, clsReadsGrowthFloor))
		}
		if c.large > clsWorkGrowthLimit*c.small {
			out = append(out, fmt.Sprintf("16 times the input gave %d %s against %d; want at most %d times as many", c.large, c.name, c.small, clsWorkGrowthLimit))
		}
	}
	return out
}
