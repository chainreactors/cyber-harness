package evaluator

import (
	"strconv"
	"strings"
)

// Budget is how long the loop may keep going. Two dials, because a round count
// is a poor way to say "dig as deep as you need": Ceiling is the hard backstop
// the loop itself enforces, and Guidance is the user's own words, handed to the
// evaluator so the continue decision follows the intent instead of a number.
type Budget struct {
	Ceiling  int
	Guidance string
}

// noisyRoundCount ignores numbers too large to be a round count — a port,
// a CIDR, a status code — so they cannot quietly raise the backstop.
const noisyRoundCount = 500

// ParseBudget reads a rounds spec. Empty takes the default ceiling; a bare
// number is a hard ceiling; anything else is natural language, passed to the
// evaluator verbatim.
//
// A number inside natural language only ever raises the backstop, never lowers
// it: "至少跑三轮" must not become a cap of three. Tightening is the
// evaluator's job — it reads the guidance and stops the loop itself — because a
// regexp cannot tell a floor from a ceiling, and guessing wrong silently cuts
// the run short.
func ParseBudget(spec string) Budget {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return Budget{Ceiling: defaultRoundCeiling}
	}
	if rounds, err := strconv.Atoi(spec); err == nil {
		if rounds <= 0 {
			return Budget{Ceiling: defaultRoundCeiling}
		}
		return Budget{Ceiling: rounds}
	}
	ceiling := defaultRoundCeiling
	for _, mentioned := range roundCounts(spec) {
		if mentioned > ceiling {
			ceiling = mentioned
		}
	}
	return Budget{Ceiling: ceiling, Guidance: spec}
}

// roundCounts collects every plausible round count in the text, in Arabic and
// in Chinese numerals — the guidance is as likely to say "最多十轮" as "max 10
// rounds", and a Chinese-only reader of the backstop would silently cap those
// runs at the default.
func roundCounts(text string) []int {
	var counts []int
	var digits strings.Builder
	flush := func() {
		if digits.Len() == 0 {
			return
		}
		if n, err := strconv.Atoi(digits.String()); err == nil && n > 0 && n <= noisyRoundCount {
			counts = append(counts, n)
		}
		digits.Reset()
	}
	for _, r := range text {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return append(counts, chineseNumbers(text)...)
}

var chineseDigits = map[rune]int{'一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}

// chineseNumbers reads the one- and two-digit forms that appear in a round
// count ("三", "十", "十五", "二十", "三十五"). Larger forms are left alone:
// past a couple of dozen rounds the exact number stops mattering.
func chineseNumbers(text string) []int {
	var (
		numbers []int
		token   []rune
	)
	flush := func() {
		if len(token) > 0 {
			if n := chineseValue(token); n > 0 {
				numbers = append(numbers, n)
			}
			token = token[:0]
		}
	}
	for _, r := range text {
		if _, ok := chineseDigits[r]; ok || r == '十' {
			token = append(token, r)
			continue
		}
		flush()
	}
	flush()
	return numbers
}

func chineseValue(token []rune) int {
	tens := -1
	for i, r := range token {
		if r != '十' {
			continue
		}
		tens = i
		break
	}
	if tens < 0 {
		if len(token) == 1 {
			return chineseDigits[token[0]]
		}
		return 0
	}
	value := 10
	if tens > 0 {
		if len(token[:tens]) != 1 {
			return 0
		}
		value = chineseDigits[token[0]] * 10
	}
	switch rest := token[tens+1:]; len(rest) {
	case 0:
	case 1:
		value += chineseDigits[rest[0]]
	default:
		return 0
	}
	return value
}
