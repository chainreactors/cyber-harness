package neutron

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/neutron/templates"
	sdkneutron "github.com/chainreactors/sdk/neutron"
	"github.com/chainreactors/sdk/pkg/association"
)

var errNoNeutronTemplates = errors.New("no neutron templates selected")

type neutronExecuteOptions struct {
	Target              string
	Templates           []*templates.Template
	RestrictToTemplates bool
	Fingers             []string
	Tags                []string
	ExcludeTags         []string
	Severities          []string
	ExcludeSeverities   []string
	IDs                 []string
	ExcludeIDs          []string
	MaxPerFinger        int
	Concurrency         int
	RateLimit           int
}

func neutronExecuteStream(ctx context.Context, engine *sdkneutron.Engine, index *association.FingerPOCIndex, opts neutronExecuteOptions) (<-chan *sdkneutron.ExecuteResult, error) {
	if engine == nil {
		return nil, errors.New("neutron engine is not available")
	}
	selected, filtered := selectNeutronTemplates(engine, index, opts)
	if filtered {
		if len(selected) == 0 {
			return nil, errNoNeutronTemplates
		}
	}
	if len(selected) == 0 {
		selected = nonNilSortedTemplates(engine.Get())
	}
	if len(selected) == 0 {
		out := make(chan *sdkneutron.ExecuteResult)
		close(out)
		return out, nil
	}

	if opts.Concurrency > 1 {
		return neutronExecuteTemplatesConcurrent(ctx, engine, opts.Target, selected, opts.Concurrency, opts.RateLimit), nil
	}

	task := sdkneutron.NewExecuteTask(opts.Target)
	task.Templates = selected
	resultCh, err := engine.Execute(sdkneutron.NewContext().WithContext(ctx), task)
	if err != nil {
		return nil, err
	}

	out := make(chan *sdkneutron.ExecuteResult)
	go func() {
		defer close(out)
		for result := range resultCh {
			execResult, ok := result.(*sdkneutron.ExecuteResult)
			if !ok {
				continue
			}
			select {
			case out <- execResult:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func neutronExecuteTemplatesConcurrent(ctx context.Context, engine *sdkneutron.Engine, target string, selected []*templates.Template, concurrency, rateLimit int) <-chan *sdkneutron.ExecuteResult {
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > len(selected) {
		concurrency = len(selected)
	}
	out := make(chan *sdkneutron.ExecuteResult)
	jobs := make(chan *templates.Template)

	var limiter <-chan time.Time
	var ticker *time.Ticker
	if rateLimit > 0 {
		interval := time.Second / time.Duration(rateLimit)
		if interval <= 0 {
			interval = time.Nanosecond
		}
		ticker = time.NewTicker(interval)
		limiter = ticker.C
	}

	go func() {
		defer close(out)
		if ticker != nil {
			defer ticker.Stop()
		}

		var wg sync.WaitGroup
		wg.Add(concurrency)
		for i := 0; i < concurrency; i++ {
			go func() {
				defer wg.Done()
				for tmpl := range jobs {
					if limiter != nil {
						select {
						case <-limiter:
						case <-ctx.Done():
							return
						}
					}
					task := sdkneutron.NewExecuteTask(target)
					task.Templates = []*templates.Template{tmpl}
					resultCh, err := engine.Execute(sdkneutron.NewContext().WithContext(ctx), task)
					if err != nil {
						continue
					}
					for result := range resultCh {
						execResult, ok := result.(*sdkneutron.ExecuteResult)
						if !ok {
							continue
						}
						select {
						case out <- execResult:
						case <-ctx.Done():
							return
						}
					}
				}
			}()
		}

		for _, tmpl := range selected {
			select {
			case jobs <- tmpl:
			case <-ctx.Done():
				close(jobs)
				wg.Wait()
				return
			}
		}
		close(jobs)
		wg.Wait()
	}()
	return out
}

func selectNeutronTemplates(engine *sdkneutron.Engine, index *association.FingerPOCIndex, opts neutronExecuteOptions) ([]*templates.Template, bool) {
	hasFingerFilter := len(opts.Fingers) > 0
	hasTagFilter := len(opts.Tags) > 0
	hasExcludeTagFilter := len(opts.ExcludeTags) > 0
	hasSeverityFilter := len(opts.Severities) > 0
	hasExcludeSeverityFilter := len(opts.ExcludeSeverities) > 0
	hasIDFilter := len(opts.IDs) > 0
	hasExcludeIDFilter := len(opts.ExcludeIDs) > 0
	hasExplicitTemplates := len(opts.Templates) > 0 || opts.RestrictToTemplates
	if !hasExplicitTemplates && !hasFingerFilter && !hasTagFilter && !hasExcludeTagFilter && !hasSeverityFilter && !hasExcludeSeverityFilter && !hasIDFilter && !hasExcludeIDFilter {
		return nil, false
	}
	if engine == nil {
		return nil, true
	}

	base := engine.Get()
	if opts.RestrictToTemplates {
		base = opts.Templates
	} else if len(opts.Templates) > 0 {
		base = append(append([]*templates.Template(nil), base...), opts.Templates...)
	}

	allowedByFinger := map[string]struct{}{}
	allowedFingers := stringSet(opts.Fingers)
	if hasFingerFilter {
		if index != nil {
			for _, finger := range opts.Fingers {
				ids := index.GetPOCsByFinger(finger)
				if opts.MaxPerFinger > 0 && len(ids) > opts.MaxPerFinger {
					ids = ids[:opts.MaxPerFinger]
				}
				for _, id := range ids {
					allowedByFinger[strings.ToLower(strings.TrimSpace(id))] = struct{}{}
				}
			}
		}
	}

	allowedTags := stringSet(opts.Tags)
	excludedTags := stringSet(opts.ExcludeTags)
	allowedSeverities := stringSet(opts.Severities)
	excludedSeverities := stringSet(opts.ExcludeSeverities)
	allowedIDs := stringSet(opts.IDs)
	excludedIDs := stringSet(opts.ExcludeIDs)

	selected := make([]*templates.Template, 0)
	seen := make(map[string]struct{})
	for _, tmpl := range base {
		if tmpl == nil {
			continue
		}
		id := strings.ToLower(strings.TrimSpace(tmpl.Id))
		if hasFingerFilter {
			if _, ok := allowedByFinger[id]; !ok && !templateHasAnyFinger(tmpl, allowedFingers) {
				continue
			}
		}
		if hasTagFilter && !templateHasAnyTag(tmpl, allowedTags) {
			continue
		}
		if hasExcludeTagFilter && templateHasAnyTag(tmpl, excludedTags) {
			continue
		}
		severity := strings.ToLower(strings.TrimSpace(tmpl.Info.Severity))
		if hasSeverityFilter {
			if _, ok := allowedSeverities[severity]; !ok {
				continue
			}
		}
		if hasExcludeSeverityFilter {
			if _, ok := excludedSeverities[severity]; ok {
				continue
			}
		}
		if hasIDFilter {
			if _, ok := allowedIDs[id]; !ok {
				continue
			}
		}
		if hasExcludeIDFilter {
			if _, ok := excludedIDs[id]; ok {
				continue
			}
		}
		key := id
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(tmpl.Info.Name))
		}
		if key != "" {
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
		}
		selected = append(selected, tmpl)
	}
	sortTemplates(selected)
	return selected, true
}

func templateHasAnyTag(tmpl *templates.Template, tags map[string]struct{}) bool {
	if len(tags) == 0 {
		return true
	}
	for _, tag := range tmpl.GetTags() {
		if _, ok := tags[strings.ToLower(strings.TrimSpace(tag))]; ok {
			return true
		}
	}
	return false
}

func templateHasAnyFinger(tmpl *templates.Template, fingers map[string]struct{}) bool {
	if len(fingers) == 0 {
		return true
	}
	for _, finger := range tmpl.Fingers {
		if _, ok := fingers[strings.ToLower(strings.TrimSpace(finger))]; ok {
			return true
		}
	}
	return false
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.ToLower(strings.TrimSpace(part))
			if part != "" {
				set[part] = struct{}{}
			}
		}
	}
	return set
}

func nonNilSortedTemplates(in []*templates.Template) []*templates.Template {
	out := make([]*templates.Template, 0, len(in))
	for _, tmpl := range in {
		if tmpl != nil {
			out = append(out, tmpl)
		}
	}
	sortTemplates(out)
	return out
}

func sortTemplates(items []*templates.Template) {
	sort.SliceStable(items, func(i, j int) bool {
		return strings.ToLower(items[i].Id) < strings.ToLower(items[j].Id)
	})
}
