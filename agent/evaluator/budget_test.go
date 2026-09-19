package evaluator

import "testing"

func TestParseBudget(t *testing.T) {
	cases := []struct {
		name     string
		spec     string
		ceiling  int
		guidance string
	}{
		{name: "empty takes the default", spec: "", ceiling: defaultRoundCeiling},
		{name: "blank takes the default", spec: "   ", ceiling: defaultRoundCeiling},
		{name: "bare number is a hard ceiling", spec: "3", ceiling: 3},
		{name: "padded number is a hard ceiling", spec: " 42 ", ceiling: 42},
		{name: "zero takes the default", spec: "0", ceiling: defaultRoundCeiling},
		{name: "negative takes the default", spec: "-5", ceiling: defaultRoundCeiling},
		{
			name:     "plain language reaches the evaluator",
			spec:     "keep going until you are sure",
			ceiling:  defaultRoundCeiling,
			guidance: "keep going until you are sure",
		},
		{
			name:     "a bigger number in guidance raises the backstop",
			spec:     "dig deep, up to 30 rounds",
			ceiling:  30,
			guidance: "dig deep, up to 30 rounds",
		},
		{
			name:     "a smaller number never lowers the backstop",
			spec:     "至少跑三轮再说",
			ceiling:  defaultRoundCeiling,
			guidance: "至少跑三轮再说",
		},
		{
			name:     "chinese numerals raise the backstop too",
			spec:     "尽量深入，最多三十五轮",
			ceiling:  35,
			guidance: "尽量深入，最多三十五轮",
		},
		{
			name:     "bare ten in chinese is read as ten",
			spec:     "最多十轮",
			ceiling:  defaultRoundCeiling,
			guidance: "最多十轮",
		},
		{
			name:     "numbers too large to be a round count are ignored",
			spec:     "scan port 8080 thoroughly",
			ceiling:  defaultRoundCeiling,
			guidance: "scan port 8080 thoroughly",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			budget := ParseBudget(c.spec)
			if budget.Ceiling != c.ceiling || budget.Guidance != c.guidance {
				t.Fatalf("ParseBudget(%q) = %+v, want ceiling %d guidance %q", c.spec, budget, c.ceiling, c.guidance)
			}
		})
	}
}
